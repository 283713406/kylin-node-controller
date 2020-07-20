FROM alpine:3.10.4
COPY ./kylin-node-controller /usr/local/bin/
ENTRYPOINT ["kylin-node-controller", "-kubeconfig=/etc/kubernetes/admin.conf", "-alsologtostderr=true"]