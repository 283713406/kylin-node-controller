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

package token

import (
	"strings"
	"time"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	bootstrapapi "k8s.io/cluster-bootstrap/token/api"
	bootstraputil "k8s.io/cluster-bootstrap/token/util"
)

// BootstrapToken describes one bootstrap token, stored as a Secret in the cluster
// TODO: The BootstrapToken object should move out to either k8s.io/client-go or k8s.io/api in the future
// (probably as part of Bootstrap Tokens going GA). It should not be staged under the kubeadm API as it is now.
type BootstrapToken struct {
	// Token is used for establishing bidirectional trust between nodes and control-planes.
	// Used for joining nodes in the cluster.
	Token *BootstrapTokenString
	// Description sets a human-friendly message why this token exists and what it's used
	// for, so other administrators can know its purpose.
	Description string
	// TTL defines the time to live for this token. Defaults to 24h.
	// Expires and TTL are mutually exclusive.
	TTL *metav1.Duration
	// Expires specifies the timestamp when this token expires. Defaults to being set
	// dynamically at runtime based on the TTL. Expires and TTL are mutually exclusive.
	Expires *metav1.Time
	// Usages describes the ways in which this token can be used. Can by default be used
	// for establishing bidirectional trust, but that can be changed here.
	Usages []string
	// Groups specifies the extra groups that this token will authenticate as when/if
	// used for authentication
	Groups []string
}

// ToSecret converts the given BootstrapToken object to its Secret representation that
// may be submitted to the API Server in order to be stored.
func (bt *BootstrapToken) ToSecret() *v1.Secret {
	return &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootstraputil.BootstrapTokenSecretName(bt.Token.ID),
			Namespace: metav1.NamespaceSystem,
		},
		Type: v1.SecretType(bootstrapapi.SecretTypeBootstrapToken),
		Data: encodeTokenSecretData(bt, time.Now()),
	}
}

// encodeTokenSecretData takes the token discovery object and an optional duration and returns the .Data for the Secret
// now is passed in order to be able to used in unit testing
func encodeTokenSecretData(token *BootstrapToken, now time.Time) map[string][]byte {
	data := map[string][]byte{
		bootstrapapi.BootstrapTokenIDKey:     []byte(token.Token.ID),
		bootstrapapi.BootstrapTokenSecretKey: []byte(token.Token.Secret),
	}

	if len(token.Description) > 0 {
		data[bootstrapapi.BootstrapTokenDescriptionKey] = []byte(token.Description)
	}

	// If for some strange reason both token.TTL and token.Expires would be set
	// (they are mutually exlusive in validation so this shouldn't be the case),
	// token.Expires has higher priority, as can be seen in the logic here.
	if token.Expires != nil {
		// Format the expiration date accordingly
		// TODO: This maybe should be a helper function in bootstraputil?
		expirationString := token.Expires.Time.Format(time.RFC3339)
		data[bootstrapapi.BootstrapTokenExpirationKey] = []byte(expirationString)

	} else if token.TTL != nil && token.TTL.Duration > 0 {
		// Only if .Expires is unset, TTL might have an effect
		// Get the current time, add the specified duration, and format it accordingly
		expirationString := now.Add(token.TTL.Duration).Format(time.RFC3339)
		data[bootstrapapi.BootstrapTokenExpirationKey] = []byte(expirationString)
	}

	for _, usage := range token.Usages {
		data[bootstrapapi.BootstrapTokenUsagePrefix+usage] = []byte("true")
	}

	if len(token.Groups) > 0 {
		data[bootstrapapi.BootstrapTokenExtraGroupsKey] = []byte(strings.Join(token.Groups, ","))
	}
	return data
}

