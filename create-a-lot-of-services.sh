#!/usr/bin/env bash

[ $# -ne 1 ] && { echo "Usage: $0 COUNT"; exit 1; }
NUM_SERVICES=$1

create-svc() {
    svc=$(cat /dev/urandom | tr -dc 'a-z' | fold -w 12 | head -n 1)
    cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Service
metadata:
  name: $svc
spec:
  selector:
    app.kubernetes.io/name: $svc
  ports:
    - protocol: TCP
      port: 80
      targetPort: 80
EOF
}

echo "Creating $NUM_SERVICES services..."
for (( i=0; i<NUM_SERVICES; i++ )); do
    create-svc
done
