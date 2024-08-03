package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

var (
	srcAddrArg         = flag.String("s", "", "Source IP address and port for DNS client. Will be inferred if not provided.")
	dstAddrArg         = flag.String("d", "", "Destination IP address and port for DNS server")
	dnsLookupNameArg   = flag.String("n", "example.com.", "DNS domain to query for, must end in '.'")
	intervalSecondsArg = flag.Int("i", 1, "Seconds to wait between DNS queries")
)

func main() {
	flag.Parse()

	err := runLoop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func runLoop() error {
	if dstAddrArg == nil || *dstAddrArg == "" {
		return fmt.Errorf("Destination address is required")
	}

	dstAddr, err := net.ResolveUDPAddr("udp", *dstAddrArg)
	if err != nil {
		return err
	}

	var srcAddr *net.UDPAddr
	if srcAddrArg != nil && *srcAddrArg != "" {
		srcAddr, err = net.ResolveUDPAddr("udp", *srcAddrArg)
		if err != nil {
			return err
		}
	} else {
		srcAddr, err = lookupSrcAddrFromInterfaces()
		if err != nil {
			return err
		}
	}

	conn, err := net.ListenUDP("udp", srcAddr)
	if err != nil {
		return err
	}

	fmt.Printf("Created UDP socket with local addr %s\n", srcAddr)

	var id uint16
	var recvBuf [65536]byte
	for {
		time.Sleep(time.Second * time.Duration(*intervalSecondsArg))

		id++ // unique ID for each req

		fmt.Printf("Sending DNS query for %q with id %d to %s\n", *dnsLookupNameArg, id, dstAddr)
		if err := sendDNSQuery(conn, dstAddr, *dnsLookupNameArg, id); err != nil {
			fmt.Printf("Error sending DNS query: %s\n", err)
			continue
		}

		// Block until DNS response received or timeout
		if err := waitForDNSResp(conn, id, recvBuf[:]); err != nil {
			fmt.Printf("Error receiving DNS resp: %s\n", err)
			continue
		}

		fmt.Printf("Deleting UDP conntrack from src IP %s\n", conn.LocalAddr())
		if err := deleteUDPConntrack(conn.LocalAddr()); err != nil {
			fmt.Printf("Error deleting conntrack: %s\n", err)
			// just keep going...
		}
	}
}

func lookupSrcAddrFromInterfaces() (*net.UDPAddr, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return &net.UDPAddr{
					IP:   ipnet.IP,
					Port: 0,
				}, nil
			}
		}
	}

	return nil, fmt.Errorf("Could not find src IP address from interfaces")
}

// Clear out any existing conntrack to repro the issue where iptables DNAT rules aren't installed
// so we create a new conntrack to the svc VIP, which blackholes the DNS traffic.
func deleteUDPConntrack(srcAddr net.Addr) error {
	srcUDPAddr, ok := srcAddr.(*net.UDPAddr)
	if !ok {
		return fmt.Errorf("Expected UDP address, but got %s", srcAddr)
	}

	// conntrack -D -p udp --src <ip> --sport <port> --dst <ip> --dst-nat
	args := []string{
		"conntrack", "-D", "-p", "udp",
		"--src", srcUDPAddr.IP.String(), "--sport", strconv.Itoa(srcUDPAddr.Port),
		"--dst-nat", // preserve the bad svc VIP entry to repro the blackhole
	}
	fmt.Printf("conntrack cmd: %v\n", args)
	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("conntrack cmd failed with output: %s\n", output)
		return err
	}

	return nil
}

func sendDNSQuery(conn *net.UDPConn, addr *net.UDPAddr, lookupName string, id uint16) error {
	queryMsg := &dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:               id,
			RecursionDesired: true,
		},
		Questions: []dnsmessage.Question{
			{
				Name:  dnsmessage.MustNewName(lookupName),
				Type:  dnsmessage.TypeA,
				Class: dnsmessage.ClassINET,
			},
		},
	}

	msgBytes, err := queryMsg.Pack()
	if err != nil {
		return err
	}

	if _, err := conn.WriteToUDP(msgBytes, addr); err != nil {
		return err
	}

	return nil
}

func waitForDNSResp(conn *net.UDPConn, id uint16, recvBuf []byte) error {
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}

	n, addr, err := conn.ReadFromUDPAddrPort(recvBuf)
	if err != nil {
		return err
	}

	msg := &dnsmessage.Message{}
	if err := msg.Unpack(recvBuf[0:n]); err != nil {
		return err
	}

	fmt.Printf("Received msg from %s with ID %d, expected %d\n", addr, msg.Header.ID, id)
	return nil
}
