package main

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/golang/glog"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	clientcertutil "k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/workqueue"

	"github.com/kylin/kylin-node-controller/cmd/constants"
	"github.com/kylin/kylin-node-controller/cmd/pubkeypin"
	kubeconfigutil "github.com/kylin/kylin-node-controller/cmd/util/kubeconfig"
	"github.com/kylin/kylin-node-controller/cmd/util/ping"
	"github.com/kylin/kylin-node-controller/cmd/util/sftp"
	"github.com/kylin/kylin-node-controller/cmd/util/ssh"
	"github.com/kylin/kylin-node-controller/cmd/util/token"
	crdv1 "github.com/kylin/kylin-node-controller/pkg/apis/crd/v1"
	clientset "github.com/kylin/kylin-node-controller/pkg/client/clientset/versioned"
	kylinnodescheme "github.com/kylin/kylin-node-controller/pkg/client/clientset/versioned/scheme"
	informers "github.com/kylin/kylin-node-controller/pkg/client/informers/externalversions/crd/v1"
	listers "github.com/kylin/kylin-node-controller/pkg/client/listers/crd/v1"
)

var joinCommandTemplate = template.Must(template.New("join").Parse(`` +
	`kubeadm join {{.ControlPlaneHostPort}} --token {{.Token}} --discovery-token-ca-cert-hash {{.CAPubKeyPins}}`,
))

var resetNodeInfo = make(map[string]*crdv1.KylinNodeSpec)

const controllerAgentName = "kylin-node-controller"

const (
	// SuccessSynced is used as part of the Event 'reason' when a KylinNode is synced
	SuccessSynced = "Synced"

	// MessageResourceSynced is the message used for an Event fired when a KylinNode
	// is synced successfully
	MessageResourceSynced = "KylinNode synced successfully"
)

// Controller is the controller implementation for KylinNode resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// kylinnodeclientset is a clientset for our own API group
	kylinnodeclientset clientset.Interface

	kylinnodesLister listers.KylinNodeLister
	kylinnodesSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

// NewController returns a new kylinNode controller
func NewController(
	kubeclientset kubernetes.Interface,
	kylinnodeclientset clientset.Interface,
	kylinNodeInformer informers.KylinNodeInformer) *Controller {

	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	utilruntime.Must(kylinnodescheme.AddToScheme(scheme.Scheme))
	glog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:    kubeclientset,
		kylinnodeclientset: kylinnodeclientset,
		kylinnodesLister:   kylinNodeInformer.Lister(),
		kylinnodesSynced:   kylinNodeInformer.Informer().HasSynced,
		workqueue:        workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "KylinNodes"),
		recorder:         recorder,
	}

	glog.Info("Setting up event handlers")
	// Set up an event handler for when KylinNode resources change
	kylinNodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueKylinNode,
		UpdateFunc: func(old, new interface{}) {
			oldKylinNode := old.(*crdv1.KylinNode)
			newKylinNode := new.(*crdv1.KylinNode)
			if oldKylinNode.ResourceVersion == newKylinNode.ResourceVersion {
				// Periodic resync will send update events for all known KylinNodes.
				// Two different versions of the same KylinNode will always have different RVs.
				return
			}
			controller.enqueueKylinNode(new)
		},
		DeleteFunc: controller.enqueueKylinNodeForDelete,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer runtime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	glog.Info("Starting KylinNode control loop")

	// Wait for the caches to be synced before starting workers
	glog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.kylinnodesSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	glog.Info("Starting workers")
	// Launch two workers to process KylinNode resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	glog.Info("Started workers")
	<-stopCh
	glog.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			runtime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// KylinNode resource to be synced.
		if err := c.syncHandler(key); err != nil {
			return fmt.Errorf("error syncing '%s': %s", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		glog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		runtime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the kylin node resource
// with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the kylinNode resource with this namespace/name
	kylinNode, err := c.kylinnodesLister.KylinNodes(namespace).Get(name)
	if err != nil {
		// The kylinNode resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			glog.Warningf("KylinNode: %s/%s does not exist in local cache, will delete it ...",
				namespace, name)

			// FIX ME: call kubeadm API to delete this kylin node by name.
			cmd := "kubeadm reset -f"
			glog.Infof("[KylinNodeController] Try to reset kylin node: %s/%s ...",
				resetNodeInfo[name].Name, resetNodeInfo[name].Address)

			user := resetNodeInfo[name].User
			pass := resetNodeInfo[name].Password
			addr := resetNodeInfo[name].Address
			if err = ssh.RunSshCommand(user, pass, addr, cmd); err != nil{
				return fmt.Errorf("[KylinNodeController] Failed reset kylin node '%s': %s", key, err.Error())
			}

			glog.Infof("[KylinNodeController] Deleting node, node name: %s ...", resetNodeInfo[name].Name)
			if err := c.kubeclientset.CoreV1().Nodes().Delete(resetNodeInfo[name].Name, nil); err != nil {
				return fmt.Errorf("unable to delete node %q: %v", resetNodeInfo[name].Name, err)
			}

			return nil
		}

		runtime.HandleError(fmt.Errorf("failed to list KylinNode by: %s/%s", namespace, name))

		return err
	}

	glog.Infof("[KylinNodeController] Try to process KylinNode: %#v ...", kylinNode)

	exitNodes, err := c.getNodeInfo()
	if err != nil {
		return err
	}

	if err := admission(kylinNode, exitNodes); err != nil {
		return fmt.Errorf("[KylinNodeController] Admission failed '%s': %s", key, err.Error())
	}

	cmd, err := getJoinNodeCommand(c.kubeclientset)
	if err != nil {
		return fmt.Errorf("[KylinNodeController] Get join node command failed '%s': %s", key, err.Error())
	}

	glog.Infof("[KylinNodeController] Join node command: %#v ...", cmd)

	// Save the node information and use when reset the node
	spec := &crdv1.KylinNodeSpec{
		Name:    kylinNode.Spec.Name,
		Address: kylinNode.Spec.Address,
		User: kylinNode.Spec.User,
		Password: kylinNode.Spec.Password,
	}
	resetNodeInfo[name] = spec
	glog.Infof("[KylinNodeController] Saved kylin node info: %#v ...", *resetNodeInfo[name])

	if err = preInstallNode(kylinNode); err != nil{
		return fmt.Errorf("[KylinNodeController] Pre install node failed '%s': %s", key, err.Error())
	}

	if err = installNode(kylinNode, cmd); err != nil{
		return fmt.Errorf("[KylinNodeController] Install node failed '%s': %s", key, err.Error())
	}

	c.recorder.Event(kylinNode, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)

	return nil
}

// enqueueKylinNode takes a KylinNod  e resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than KylinNode.
func (c *Controller) enqueueKylinNode(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		runtime.HandleError(err)
		return
	}
	c.workqueue.AddRateLimited(key)
}

// enqueueKylinNodeForDelete takes a deleted KylinNode resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than KylinNode.
func (c *Controller) enqueueKylinNodeForDelete(obj interface{}) {
	var key string
	var err error
	key, err = cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}
	c.workqueue.AddRateLimited(key)
}

func (c *Controller) getNodeInfo() (map[string]string, error) {
	nodes, err := c.kubeclientset.CoreV1().Nodes().List(metav1.ListOptions{ResourceVersion: "0"})
	if err != nil {
		glog.Errorf("Error found node status: %v", err)
		return nil, err
	}

	// exitNodes is map, key is node name, value is node address
	exitNodes := make(map[string]string)
	for i := range nodes.Items {
		for j := range nodes.Items[i].Status.Addresses {
			if nodes.Items[i].Status.Addresses[j].Type == corev1.NodeInternalIP {
				nodeName := nodes.Items[i].Name
				nodeAddress := nodes.Items[i].Status.Addresses[j].Address
				glog.Infof("[KylinNodeController] Found node, node name is: %#v, node address is %v",
					nodeName, nodeAddress)
				exitNodes[nodeName] = nodeAddress
			}
		}
	}

	return exitNodes, nil
}

// admission join node
func admission(kylinNode *crdv1.KylinNode, exitNodes map[string]string) error {
	glog.Infof("[KylinNodeController] Found kylin node, node name is: %#v, node address is %v",
		kylinNode.Spec.Name, kylinNode.Spec.Address)

	for k, v := range exitNodes {
		if k == kylinNode.Spec.Name || v == kylinNode.Spec.Address {
			return fmt.Errorf("The node [%s_%v] is exited in cluster ", k, v)
		}
	}

	if ok := ping.AddrAccessible(kylinNode.Spec.Address); ok{
		glog.Infof("Connect address %s success", kylinNode.Spec.Address)
		return nil
	}else {
		return fmt.Errorf("Connect address %s failed ", kylinNode.Spec.Address)
	}

	return nil
}

// getJoinNodeCommand get joinNode command
// kubeadm join 10.9.8.134:6443 --token d7ms6b.sezwmg8chxx0r7ni
// --discovery-token-ca-cert-hash sha256:71bc95b5777a3a175d9b8787b43e97613325e5e91250bac62031bbbd7811c0a0
func getJoinNodeCommand(kubeClient kubernetes.Interface) (string, error) {
	validToken, ok, err := token.GetValidToken(kubeClient)
	if err!= nil{
		return "", fmt.Errorf("failed to get valid token, error: %v ", err)
	}
	if ok {
		glog.Infof("valid token name is: %v", validToken)
	}else {
		glog.Infof("no valid token, create new token")
		token, err := token.CreateShortLivedBootstrapToken(kubeClient)
		if err != nil {
			return "", fmt.Errorf("failed to get token, error: %v ", err)
		}
		validToken = token
	}

	kubeConfigFile := constants.GetAdminKubeConfigPath()
	// load the kubeconfig file to get the CA certificate and endpoint
	config, err := clientcmd.LoadFromFile(kubeConfigFile)
	if err != nil {
		return "", fmt.Errorf("failed to load kubeconfig, error: %v ", err)
	}

	// load the default cluster config
	clusterConfig := kubeconfigutil.GetClusterFromKubeConfig(config)
	if clusterConfig == nil {
		return "", fmt.Errorf("failed to get default cluster config")
	}

	// load CA certificates from the kubeconfig (either from PEM data or by file path)
	var caCerts []*x509.Certificate
	if clusterConfig.CertificateAuthorityData != nil {
		caCerts, err = clientcertutil.ParseCertsPEM(clusterConfig.CertificateAuthorityData)
		if err != nil {
			return "", fmt.Errorf("failed to parse CA certificate from kubeconfig, error: %v", err)
		}
	} else if clusterConfig.CertificateAuthority != "" {
		caCerts, err = clientcertutil.CertsFromFile(clusterConfig.CertificateAuthority)
		if err != nil {
			return "", fmt.Errorf("failed to load CA certificate referenced by kubeconfig, error: %v", err)
		}
	} else {
		return "", fmt.Errorf("no CA certificates found in kubeconfig")
	}

	// hash all the CA certs and include their public key pins as trusted values
	publicKeyPins := make([]string, 0, len(caCerts))
	for _, caCert := range caCerts {
		publicKeyPins = append(publicKeyPins, pubkeypin.Hash(caCert))
	}

	ctx := map[string]interface{}{
		"Token":                strings.Replace(validToken, "\n", "", -1),
		"CAPubKeyPins":         publicKeyPins[0],
		"ControlPlaneHostPort": strings.Replace(clusterConfig.Server, "https://", "", -1),
	}
	glog.Infof("[KylinNodeController] Token is: %s", validToken)
	glog.Infof("[KylinNodeController] publicKeyPins is: %s", publicKeyPins[0])

	var out bytes.Buffer
	err = joinCommandTemplate.Execute(&out, ctx)
	if err != nil {
		return "",  fmt.Errorf("failed to render join command template, error: %v", err)
	}

	return out.String(), nil
}

func preInstallNode(kylinNode *crdv1.KylinNode) error {
	glog.Infof("[KylinNodeController] preinstall node, node name is: %#v, node address is %v",
		kylinNode.Spec.Name, kylinNode.Spec.Address)

	// rename host name
	name := kylinNode.Spec.Name
	user := kylinNode.Spec.User
	pass := kylinNode.Spec.Password
	addr := kylinNode.Spec.Address
	cmd := fmt.Sprintf("hostnamectl set-hostname %s", kylinNode.Spec.Name)
	if err := ssh.RunSshCommand(user, pass, addr, cmd); err != nil{
		return fmt.Errorf("Failed preInstall node, error: %v ", err.Error())
	}

	// copy init node file
	srcFile := constants.KylinInitNodeFile
	desPath := constants.KylinHomePath
	if err := sftp.CopyFileBySftp(user, pass, addr, srcFile, desPath); err != nil {
		return fmt.Errorf("Failed preInstall node, copy init_node file error: %v ", err.Error())
	}
	glog.Infof("[KylinNodeController] preinstall node, copy init_node file to remote server %s/%s success!",
		name, addr)

	unzipcmd := "[ -f \"/home/kylin/init_node.zip\" ] && unzip -o -d /home/kylin/ /home/kylin/init_node.zip"
	if err := ssh.RunSshCommand(user, pass, addr, unzipcmd); err != nil {
		return fmt.Errorf("Failed preInstall node, unzip init_node file error: %v ", err.Error())
	}
	glog.Infof("[KylinNodeController] preinstall node, unzip init_node file to remote server %s/%s success!",
		name, addr)

	glog.Infof("[KylinNodeController] preinstall node, Start init_node to remote server %s/%s ...", name, addr)
	initcmd := "[ -f \"/home/kylin/init_node/init.sh\" ] && bash -x /home/kylin/init_node/init.sh"
	if err := ssh.RunSshCommand(user, pass, addr, initcmd); err != nil {
		return fmt.Errorf("Failed preInstall node, run init node file error: %v ", err.Error())
	}

	glog.Infof("[KylinNodeController] preinstall node success, node name is: %#v, node address is %v",
		name, addr)

	return nil
}

func installNode(kylinNode *crdv1.KylinNode, cmd string) error {
	glog.Infof("[KylinNodeController] Start install node, node name is: %#v, node address is %v",
		kylinNode.Spec.Name, kylinNode.Spec.Address)
	if err := ssh.RunSshCommand(kylinNode.Spec.User, kylinNode.Spec.Password, kylinNode.Spec.Address, cmd); err != nil{
		return fmt.Errorf("Failed install node, error: %v ", err.Error())
	}
	glog.Infof("[KylinNodeController] Install node success, node name is: %#v, node address is %v",
		kylinNode.Spec.Name, kylinNode.Spec.Address)
	return nil
}

/*
func (c *Controller) updateKylinNodeStatus(kylinNode *crdv1.KylinNode, phase crdv1.KylinNodePhase, message, namespace string) error {
	kylinNode = kylinNode.DeepCopy()

	kylinNode.Status.Phase = phase
	kylinNode.Status.Message = message

	_, err := c.kylinnodeclientset.CrdV1().KylinNodes(namespace).UpdateStatus(kylinNode)
	if err != nil {
		return fmt.Errorf("Failed update kylin node status, error: %v ", err.Error())
	}

	return nil
}*/