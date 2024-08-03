# dns-blackhole-tester

DNS client to reproduce kube-proxy DNS blackhole bug

## Background

This repo contains the test client and scripts to reproduce this kube-proxy bug:
https://github.com/kubernetes/kubernetes/issues/126468


## DNS client (Go program)

`main.go` has the code for a DNS client to reproduce the bug. The important parts are:

1. It reuses the same UDP socket for all DNS queries, so the src IP and port will be the same.
   If a conntrack entry is created without DNAT (i.e. directly to the kube-dns service VIP), then
   subsequent queries from the same client will reuse that conntrack entry, blackholing traffic.

2. It deletes any UDP DNAT conntrack entries for its src IP / port. This increases the likelihood
   of a DNS query happening when kube-proxy has NOT installed the DNAT iptables rules for kube-dns,
   which triggers the bug.

3. Query every 1 second. This is fast enough that it will keep any UDP conntrack entries alive,
   AND that a DNS query will occur during kube-proxy restart when the DNAT iptables rules for kube-dns
   are incorrectly removed.


## Script to create a lot of services

`create-a-lot-of-services.sh` is a bash script to create a large number of Kubernetes services with random names.
I was able to reproduce the bug with about 2,000 services.


## Daemonset manifest

`ds.yaml` is the daemonset for running the dns-blackhole-tester DNS client as a daemonset on every node.
Important parts:

1. Needs NET_ADMIN, NET_RAW, and host network so it can delete DNAT conntrack entries.
2. The "image" part is empty -- replace that with an image in some container registry your k8s cluster can pull from.
3. It uses default kube-dns svc VIP 10.0.0.10 and queries for "kubernetes.default.svc.cluster.local" which should be resolved directly by coredns.

## Steps to repro the bug

1. Create a Kubernetes cluster. I did it like this using AKS (using Azure CNI Overlay, which has kube-proxy installed):
```
RESOURCE_GROUP=<your resource group>
CLUSTER=<your cluster>
az aks create -g $RESOURCE_GROUP -n $CLUSTER --network-plugin azure --network-plugin-mode overlay --node-count 5
az aks get-credentials -g $RESOURCE_GROUP -n $CLUSTER
```

2. Build the DNS client image and push to a container registry:
```
REGISTRY=<your container registry>
docker build . -t $REGISTRY/dns-blackhole-tester:v0.0.1
docker push $REGISTRY/dns-blackhole-tester:v0.0.1
```

3. Install the daemonset:
```
# first update the manifest with the image/registry
kubectl apply -f ds.yaml
```

4. Install a lot of k8s services:
```
./create-a-lot-of-services.sh 2000
```

5. Tail the logs of dns-blackhole-tester looking for errors:
```
kubectl logs -f -l name=dns-blackhole-tester --timestamps --tail 100 | grep -i error
```

6. Repeatedly restart kube-proxy to trigger the bug.
```
kubectl rollout restart -n kube-system ds/kube-proxy
```

## What I saw when I tested this

Repro'd in Kubernetes 1.29.7 cluster.

After restarting kube-proxy a few times, dns-blackhole-tester logs showed errors:
```
2024-08-03T19:14:46.265162392Z Error receiving DNS resp: read udp 10.224.0.8:36009: i/o timeout
2024-08-03T19:14:57.272414560Z Error receiving DNS resp: read udp 10.224.0.8:36009: i/o timeout
2024-08-03T19:15:08.279780246Z Error receiving DNS resp: read udp 10.224.0.8:36009: i/o timeout
2024-08-03T19:15:19.287819276Z Error receiving DNS resp: read udp 10.224.0.8:36009: i/o timeout
```

Errors continue even after kube-proxy has finished starting.

Checking conntrack on the node with IP 10.224.0.8, I see:
```
root@aks-nodes-41305373-vmss000000:/# conntrack -L -p udp | grep UNREPL
conntrack v1.4.6 (conntrack-tools): 7 flow entries have been shown.
udp      17 22 src=10.224.0.8 dst=10.0.0.10 sport=36009 dport=53 [UNREPLIED] src=10.0.0.10 dst=10.224.0.8 sport=53 dport=36009 mark=0 use=1
```
which exactly matches the behavior reported in https://github.com/kubernetes/kubernetes/issues/126468

Deleting the conntrack entry, the errors go away:
```
root@aks-nodes-41305373-vmss000000:/# conntrack -D -p udp --src 10.224.0.8 --sport 36009
udp      17 28 src=10.224.0.8 dst=10.0.0.10 sport=36009 dport=53 [UNREPLIED] src=10.0.0.10 dst=10.224.0.8 sport=53 dport=36009 mark=0 use=1
conntrack v1.4.6 (conntrack-tools): 1 flow entries have been deleted.
```
