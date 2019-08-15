/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package etcd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"k8s.io/klog"

	"k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/images"
	kubeadmutil "k8s.io/kubernetes/cmd/kubeadm/app/util"
	etcdutil "k8s.io/kubernetes/cmd/kubeadm/app/util/etcd"
	staticpodutil "k8s.io/kubernetes/cmd/kubeadm/app/util/staticpod"
)

const (
	etcdVolumeName           = "etcd-data"
	certsVolumeName          = "etcd-certs"
	etcdHealthyCheckInterval = 5 * time.Second
	etcdHealthyCheckRetries  = 8
)

// CreateLocalEtcdStaticPodManifestFile will write local etcd static pod manifest file.
// This function is used by init - when the etcd cluster is empty - or by kubeadm
// upgrade - when the etcd cluster is already up and running (and the --initial-cluster flag have no impact)
func CreateLocalEtcdStaticPodManifestFile(manifestDir string, nodeName string, cfg *kubeadmapi.ClusterConfiguration, endpoint *kubeadmapi.APIEndpoint) error {
	if cfg.Etcd.External != nil {
		return errors.New("etcd static pod manifest cannot be generated for cluster using external etcd")
	}
	// gets etcd StaticPodSpec
	spec := GetEtcdPodSpec(cfg, endpoint, nodeName, []etcdutil.Member{})

	// writes etcd StaticPod to disk
	if err := staticpodutil.WriteStaticPodToDisk(kubeadmconstants.Etcd, manifestDir, spec); err != nil {
		return err
	}

	klog.V(1).Infof("[etcd] wrote Static Pod manifest for a local etcd member to %q\n", kubeadmconstants.GetStaticPodFilepath(kubeadmconstants.Etcd, manifestDir))
	return nil
}

// CheckLocalEtcdClusterStatus verifies health state of local/stacked etcd cluster before installing a new etcd member
func CheckLocalEtcdClusterStatus(client clientset.Interface, cfg *kubeadmapi.ClusterConfiguration) error {
	klog.V(1).Info("[etcd] Checking etcd cluster health")

	// creates an etcd client that connects to all the local/stacked etcd members
	klog.V(1).Info("creating etcd client that connects to etcd pods")
	etcdClient, err := etcdutil.NewFromCluster(client, cfg.CertificatesDir)
	if err != nil {
		return err
	}

	// Checking health state
	_, err = etcdClient.GetClusterStatus()
	if err != nil {
		return errors.Wrap(err, "etcd cluster is not healthy")
	}

	return nil
}

// RemoveStackedEtcdMemberFromCluster will remove a local etcd member from etcd cluster,
// when reset the control plane node.
func RemoveStackedEtcdMemberFromCluster(client clientset.Interface, cfg *kubeadmapi.InitConfiguration) error {
	// creates an etcd client that connects to all the local/stacked etcd members
	klog.V(1).Info("[etcd] creating etcd client that connects to etcd pods")
	etcdClient, err := etcdutil.NewFromCluster(client, cfg.CertificatesDir)
	if err != nil {
		return err
	}

	// notifies the other members of the etcd cluster about the removing member
	etcdPeerAddress := etcdutil.GetPeerURL(&cfg.LocalAPIEndpoint)

	klog.V(2).Infof("[etcd] get the member id from peer: %s", etcdPeerAddress)
	id, err := etcdClient.GetMemberID(etcdPeerAddress)
	if err != nil {
		return err
	}

	klog.V(1).Infof("[etcd] removing etcd member: %s, id: %d", etcdPeerAddress, id)
	members, err := etcdClient.RemoveMember(id)
	if err != nil {
		return err
	}
	klog.V(1).Infof("[etcd] Updated etcd member list: %v", members)

	return nil
}

// CreateStackedEtcdStaticPodManifestFile will write local etcd static pod manifest file
// for an additional etcd member that is joining an existing local/stacked etcd cluster.
// Other members of the etcd cluster will be notified of the joining node in beforehand as well.
func CreateStackedEtcdStaticPodManifestFile(client clientset.Interface, manifestDir string, nodeName string, cfg *kubeadmapi.ClusterConfiguration, endpoint *kubeadmapi.APIEndpoint) error {
	// creates an etcd client that connects to all the local/stacked etcd members
	klog.V(1).Info("creating etcd client that connects to etcd pods")
	etcdClient, err := etcdutil.NewFromCluster(client, cfg.CertificatesDir)
	if err != nil {
		return err
	}

	// notifies the other members of the etcd cluster about the joining member
	etcdPeerAddress := etcdutil.GetPeerURL(endpoint)

	klog.V(1).Infof("Adding etcd member: %s", etcdPeerAddress)
	initialCluster, err := etcdClient.AddMember(nodeName, etcdPeerAddress)
	if err != nil {
		return err
	}
	fmt.Println("[etcd] Announced new etcd member joining to the existing etcd cluster")
	klog.V(1).Infof("Updated etcd member list: %v", initialCluster)

	klog.V(1).Info("Creating local etcd static pod manifest file")
	// gets etcd StaticPodSpec, actualized for the current InitConfiguration and the new list of etcd members
	spec := GetEtcdPodSpec(cfg, endpoint, nodeName, initialCluster)
	// writes etcd StaticPod to disk
	if err := staticpodutil.WriteStaticPodToDisk(kubeadmconstants.Etcd, manifestDir, spec); err != nil {
		return err
	}

	fmt.Printf("[etcd] Wrote Static Pod manifest for a local etcd member to %q\n", kubeadmconstants.GetStaticPodFilepath(kubeadmconstants.Etcd, manifestDir))

	fmt.Printf("[etcd] Waiting for the new etcd member to join the cluster. This can take up to %v\n", etcdHealthyCheckInterval*etcdHealthyCheckRetries)
	if _, err := etcdClient.WaitForClusterAvailable(etcdHealthyCheckRetries, etcdHealthyCheckInterval); err != nil {
		return err
	}

	return nil
}


// GetEtcdPodSpec returns the etcd static Pod actualized to the context of the current configuration
// NB. GetEtcdPodSpec methods holds the information about how kubeadm creates etcd static pod manifests.
func GetEtcdPodSpec(cfg *kubeadmapi.ClusterConfiguration, endpoint *kubeadmapi.APIEndpoint, nodeName string, initialCluster []etcdutil.Member) v1.Pod {
	pathType := v1.HostPathDirectoryOrCreate
	etcddatapathtype:= v1.HostPathFileOrCreate
	etcdMounts := map[string]v1.Volume{
		etcdVolumeName:  staticpodutil.NewVolume(etcdVolumeName, "C:/etcd.vhdx", &etcddatapathtype),
		certsVolumeName: staticpodutil.NewVolume(certsVolumeName, cfg.CertificatesDir+"/etcd", &pathType),
	}
	return staticpodutil.ComponentPod(v1.Container{
		Name:            kubeadmconstants.Etcd,
		Command:         getEtcdCommand(cfg, endpoint, nodeName, initialCluster),
		Image:           images.GetEtcdImage(cfg),
		ImagePullPolicy: v1.PullIfNotPresent,
		Ports:           []v1.ContainerPort{
			             	{
			            		HostPort: 2379, ContainerPort: 2379,
						 	},
		                },
		// Mount the etcd datadir path read-write so etcd can store data in a more persistent manner
		VolumeMounts: []v1.VolumeMount{
			staticpodutil.NewVolumeMount(etcdVolumeName, "/var/lib/etcd", false),
			staticpodutil.NewVolumeMount(certsVolumeName, "/etc/kubernetes/pki/etcd", false),
		},
		LivenessProbe: staticpodutil.EtcdProbe(
			&cfg.Etcd, kubeadmconstants.EtcdListenClientPort, cfg.CertificatesDir,
			kubeadmconstants.EtcdCACertName, kubeadmconstants.EtcdHealthcheckClientCertName, kubeadmconstants.EtcdHealthcheckClientKeyName,
		),
	}, etcdMounts)
}

// getEtcdCommand builds the right etcd command from the given config object
func getEtcdCommand(cfg *kubeadmapi.ClusterConfiguration, endpoint *kubeadmapi.APIEndpoint, nodeName string, initialCluster []etcdutil.Member) []string {
	defaultArguments := map[string]string{
		"name":                        nodeName,
		"listen-client-urls":          fmt.Sprintf("%s", etcdutil.GetClientURLByIP("0.0.0.0")),
		"advertise-client-urls":       "https://0.0.0.0:2379",
		"data-dir":                    "/var/lib/etcd",
		"cert-file":                   strings.Replace((filepath.Join(kubeadmconstants.CertDir, kubeadmconstants.EtcdServerCertName)),  "\\", "/", -1),
		"key-file":                    strings.Replace((filepath.Join(kubeadmconstants.CertDir, kubeadmconstants.EtcdServerKeyName)),  "\\", "/", -1),
		"trusted-ca-file":             strings.Replace((filepath.Join(kubeadmconstants.CertDir, kubeadmconstants.EtcdCACertName)),  "\\", "/", -1),
		"client-cert-auth":            "true",
		"peer-cert-file":              strings.Replace((filepath.Join(kubeadmconstants.CertDir, kubeadmconstants.EtcdPeerCertName)),  "\\", "/", -1),
		"peer-key-file":               strings.Replace((filepath.Join(kubeadmconstants.CertDir, kubeadmconstants.EtcdPeerKeyName)),  "\\", "/", -1),
		"peer-trusted-ca-file":        strings.Replace((filepath.Join(kubeadmconstants.CertDir, kubeadmconstants.EtcdCACertName)),  "\\", "/", -1),
		"peer-client-cert-auth":       "true",
		"snapshot-count":              "10000",
	}

	if len(initialCluster) == 0 {
		//defaultArguments["initial-cluster"] = fmt.Sprintf("%s=%s", nodeName, "https://127.0.0.1:2380")
	} else {
		// NB. the joining etcd member should be part of the initialCluster list
		endpoints := []string{}
		for _, member := range initialCluster {
			endpoints = append(endpoints, fmt.Sprintf("%s=%s", member.Name, member.PeerURL))
		}

		defaultArguments["initial-cluster"] = strings.Join(endpoints, ",")
		defaultArguments["initial-cluster-state"] = "existing"
	}

	command := []string{"etcd"}
	command = append(command, kubeadmutil.BuildArgumentListFromMap(defaultArguments, cfg.Etcd.Local.ExtraArgs)...)
	return command
}
