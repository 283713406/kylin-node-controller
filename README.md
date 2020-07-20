功能：用于k8s集群纳管

使用步骤：

1、编译 
go build -o kylin-node-controller .

2、运行
./kylin-node-controller  -kubeconfig=$HOME/.kube/config -alsologtostderr=true

3、创建crd
kubectl apply -f ./crd/node.yaml 

4、创建KylinNode资源
kubectl apply -f ./example/example-node.yaml
