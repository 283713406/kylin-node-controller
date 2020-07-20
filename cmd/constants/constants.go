package constants

import (
	"path/filepath"
)


const (
	// KubernetesDir is the directory Kubernetes owns for storing various configuration files
	KubernetesDir = "/etc/kubernetes"

	// AdminKubeConfigFileName defines name for the kubeconfig aimed to be used by the superuser/admin of the cluster
	AdminKubeConfigFileName = "admin.conf"

	SshPort = 22

	KylinInitNodeFile = "/home/kylin/init_node.zip"

	KylinHomePath = "/home/kylin/"

	ICMPCOUNT = 5

	PINGTIME = 100
)

// GetAdminKubeConfigPath returns the location on the disk where admin kubeconfig is located by default
func GetAdminKubeConfigPath() string {
	return filepath.Join(KubernetesDir, AdminKubeConfigFileName)
}