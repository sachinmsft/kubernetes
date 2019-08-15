// +build windows

/*
Copyright 2018 The Kubernetes Authors.

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

package v1beta1

const (
	// DefaultCACertPath defines default location of CA certificate on Windows
	DefaultCACertPath = "C:/etc/kubernetes/pki/ca.crt"
	// DefaultUrlScheme defines default socket url prefix
	DefaultUrlScheme = "npipe"
	// DefaultCertificatesDir defines default certificate directory
	DefaultCertificatesDir = "C:/etc/kubernetes/pki"
	// DefaultManifestsDir defines default manifests directory
	DefaultManifestsDir = "C:/etc/kubernetes/manifests"
	// DefaultEtcdDataDir defines default location of etcd where static pods will save data to
	DefaultEtcdDataDir = "C:/var/lib/etcd"
)
