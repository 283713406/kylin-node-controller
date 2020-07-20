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
	bootstraputil "k8s.io/cluster-bootstrap/token/util"
)

type BootstrapTokenString struct {
	ID     string
	Secret string
}

// String returns the string representation of the BootstrapTokenString
func (bts BootstrapTokenString) String() string {
	if len(bts.ID) > 0 && len(bts.Secret) > 0 {
		return bootstraputil.TokenFromIDAndSecret(bts.ID, bts.Secret)
	}
	return ""
}


