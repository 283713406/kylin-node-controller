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

package token

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	"k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	bootstrapapi "k8s.io/cluster-bootstrap/token/api"
	bootstraputil "k8s.io/cluster-bootstrap/token/util"
)

// DefaultCertTokenDuration specifies the default amount of time that the token used by upload certs will be valid
// Default behaviour is 2 hours
const (
	defaultCertTokenDuration = 2 * time.Hour

	// bootstrapTokenIDBytes defines the number of bytes used for the Bootstrap Token's ID field
	bootstrapTokenIDBytes = 6

	// bootstrapTokenSecretBytes defines the number of bytes used the Bootstrap Token's Secret field
	bootstrapTokenSecretBytes = 16

	// validBootstrapTokenChars defines the characters a bootstrap token can consist of
	validBootstrapTokenChars = "0123456789abcdefghijklmnopqrstuvwxyz"
)

var (
	tokenUsage = []string{"authentication", "signing"}
	tokenGroups = []string{"system:bootstrappers:kubeadm:default-node-token"}
) 

func CreateShortLivedBootstrapToken(client clientset.Interface) (string, error) {
	tokenStr, err := generateBootstrapToken()
	if err != nil {
		return "", errors.Wrap(err, "error generating token to upload certs")
	}
	token, err := newBootstrapTokenString(tokenStr)
	if err != nil {
		return "", errors.Wrap(err, "error creating upload certs token")
	}

	tokens := []BootstrapToken{{
		Token:       token,
		Usages: tokenUsage,
		TTL: &metav1.Duration{
			Duration: defaultCertTokenDuration,
		},
		Groups: tokenGroups,
	}}

	if err := createNewTokens(client, tokens); err != nil {
		return "", errors.Wrap(err, "error creating token")
	}

	return tokens[0].Token.String(), nil
}

// CreateNewTokens tries to create a token and fails if one with the same ID already exists
func createNewTokens(client clientset.Interface, tokens []BootstrapToken) error {
	return updateOrCreateTokens(client, true, tokens)
}

// UpdateOrCreateTokens attempts to update a token with the given ID, or create if it does not already exist.
func updateOrCreateTokens(client clientset.Interface, failIfExists bool, tokens []BootstrapToken) error {

	for _, token := range tokens {

		secretName := bootstraputil.BootstrapTokenSecretName(token.Token.ID)
		secret, err := client.CoreV1().Secrets(metav1.NamespaceSystem).Get(secretName, metav1.GetOptions{})
		if secret != nil && err == nil && failIfExists {
			return errors.Errorf("a token with id %q already exists", token.Token.ID)
		}

		updatedOrNewSecret := token.ToSecret()
		// Try to create or update the token with an exponential backoff
		err = tryRunCommand(func() error {
			if err := createOrUpdateSecret(client, updatedOrNewSecret); err != nil {
				return errors.Wrapf(err, "failed to create or update bootstrap token with name %s", secretName)
			}
			return nil
		}, 5)
		if err != nil {
			return err
		}
	}
	return nil
}

// TryRunCommand runs a function a maximum of failureThreshold times, and retries on error. If failureThreshold is hit; the last error is returned
func tryRunCommand(f func() error, failureThreshold int) error {
	backoff := wait.Backoff{
		Duration: 5 * time.Second,
		Factor:   2, // double the timeout for every failure
		Steps:    failureThreshold,
	}
	return wait.ExponentialBackoff(backoff, func() (bool, error) {
		err := f()
		if err != nil {
			// Retry until the timeout
			return false, nil
		}
		// The last f() call was a success, return cleanly
		return true, nil
	})
}

// CreateOrUpdateSecret creates a Secret if the target resource doesn't exist. If the resource exists already, this function will update the resource instead.
func createOrUpdateSecret(client clientset.Interface, secret *v1.Secret) error {
	if _, err := client.CoreV1().Secrets(secret.ObjectMeta.Namespace).Create(secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return errors.Wrap(err, "unable to create secret")
		}

		if _, err := client.CoreV1().Secrets(secret.ObjectMeta.Namespace).Update(secret); err != nil {
			return errors.Wrap(err, "unable to update secret")
		}
	}
	return nil
}

// NewBootstrapTokenString converts the given Bootstrap Token as a string
// to the BootstrapTokenString object used for serialization/deserialization
// and internal usage. It also automatically validates that the given token
// is of the right format
func newBootstrapTokenString(token string) (*BootstrapTokenString, error) {
	substrs := bootstraputil.BootstrapTokenRegexp.FindStringSubmatch(token)
	// TODO: Add a constant for the 3 value here, and explain better why it's needed (other than because how the regexp parsin works)
	if len(substrs) != 3 {
		return nil, errors.Errorf("the bootstrap token %q was not of the form %q", token, bootstrapapi.BootstrapTokenPattern)
	}

	return &BootstrapTokenString{ID: substrs[1], Secret: substrs[2]}, nil
}

// GenerateBootstrapToken generates a new, random Bootstrap Token.
func generateBootstrapToken() (string, error) {
	tokenID, err := randBytes(bootstrapTokenIDBytes)
	if err != nil {
		return "", err
	}

	tokenSecret, err := randBytes(bootstrapTokenSecretBytes)
	if err != nil {
		return "", err
	}

	return tokenFromIDAndSecret(tokenID, tokenSecret), nil
}

// TokenFromIDAndSecret returns the full token which is of the form "{id}.{secret}"
func tokenFromIDAndSecret(id, secret string) string {
	return fmt.Sprintf("%s.%s", id, secret)
}

// randBytes returns a random string consisting of the characters in
// validBootstrapTokenChars, with the length customized by the parameter
func randBytes(length int) (string, error) {
	// len("0123456789abcdefghijklmnopqrstuvwxyz") = 36 which doesn't evenly divide
	// the possible values of a byte: 256 mod 36 = 4. Discard any random bytes we
	// read that are >= 252 so the bytes we evenly divide the character set.
	const maxByteValue = 252

	var (
		b     byte
		err   error
		token = make([]byte, length)
	)

	reader := bufio.NewReaderSize(rand.Reader, length*2)
	for i := range token {
		for {
			if b, err = reader.ReadByte(); err != nil {
				return "", err
			}
			if b < maxByteValue {
				break
			}
		}

		token[i] = validBootstrapTokenChars[int(b)%len(validBootstrapTokenChars)]
	}

	return string(token), nil
}

func GetValidToken(client clientset.Interface) (string, bool, error) {
	tokens, err := runListTokens(client)
	glog.Infof("Found num %v token", len(tokens))
	if err != nil {
		return "", false, errors.Wrap(err, "failed to list bootstrap tokens")
	}

	if len(tokens) == 0 {
		return "", false, nil
	}

	var validTokens []string

	for _, token := range tokens {
		if reflect.DeepEqual(tokenUsage, token.Usages) && reflect.DeepEqual(tokenGroups, token.Groups){
			validTokens = append(validTokens, tokenFromIDAndSecret(token.Token.ID, token.Token.Secret))
		}
	}

	if len(validTokens) == 0 {
		return "", false, nil
	}

	return validTokens[0], true, nil
}

// RunListTokens lists details on all existing bootstrap tokens on the server.
func runListTokens(client clientset.Interface) ([]BootstrapToken, error) {
	// First, build our selector for bootstrap tokens only
	glog.Infoln("[token] preparing selector for bootstrap token")
	tokenSelector := fields.SelectorFromSet(
		map[string]string{
			// TODO: We hard-code "type" here until `field_constants.go` that is
			// currently in `pkg/apis/core/` exists in the external API, i.e.
			// k8s.io/api/v1. Should be v1.SecretTypeField
			"type": string(bootstrapapi.SecretTypeBootstrapToken),
		},
	)
	listOptions := metav1.ListOptions{
		FieldSelector: tokenSelector.String(),
	}

	var tokens []BootstrapToken

	glog.Infoln("[token] retrieving list of bootstrap tokens")
	secrets, err := client.CoreV1().Secrets(metav1.NamespaceSystem).List(listOptions)
	if err != nil {
		return tokens, errors.Wrap(err, "failed to list bootstrap tokens")
	}

	for _, secret := range secrets.Items {

		// Get the BootstrapToken struct representation from the Secret object
		token, err := BootstrapTokenFromSecret(&secret)
		if err != nil {
			glog.Errorf("Failed get bootstrapToken from secret: %v", err)
			continue
		}
		tokens = append(tokens, *token)
	}

	return tokens, nil
}

// BootstrapTokenFromSecret returns a BootstrapToken object from the given Secret
func BootstrapTokenFromSecret(secret *v1.Secret) (*BootstrapToken, error) {
	// Get the Token ID field from the Secret data
	tokenID := getSecretString(secret, bootstrapapi.BootstrapTokenIDKey)
	if len(tokenID) == 0 {
		return nil, errors.Errorf("bootstrap Token Secret has no token-id data: %s", secret.Name)
	}

	// Enforce the right naming convention
	if secret.Name != bootstraputil.BootstrapTokenSecretName(tokenID) {
		return nil, errors.Errorf("bootstrap token name is not of the form '%s(token-id)'. Actual: %q. Expected: %q",
			bootstrapapi.BootstrapTokenSecretPrefix, secret.Name, bootstraputil.BootstrapTokenSecretName(tokenID))
	}

	tokenSecret := getSecretString(secret, bootstrapapi.BootstrapTokenSecretKey)
	if len(tokenSecret) == 0 {
		return nil, errors.Errorf("bootstrap Token Secret has no token-secret data: %s", secret.Name)
	}

	// Create the BootstrapTokenString object based on the ID and Secret
	bts, err := newBootstrapTokenStringFromIDAndSecret(tokenID, tokenSecret)
	if err != nil {
		return nil, errors.Wrap(err, "bootstrap Token Secret is invalid and couldn't be parsed")
	}

	// Get the description (if any) from the Secret
	description := getSecretString(secret, bootstrapapi.BootstrapTokenDescriptionKey)

	// Expiration time is optional, if not specified this implies the token
	// never expires.
	secretExpiration := getSecretString(secret, bootstrapapi.BootstrapTokenExpirationKey)
	var expires *metav1.Time
	if len(secretExpiration) > 0 {
		expTime, err := time.Parse(time.RFC3339, secretExpiration)
		if err != nil {
			return nil, errors.Wrapf(err, "can't parse expiration time of bootstrap token %q", secret.Name)
		}
		expires = &metav1.Time{Time: expTime}
	}

	// Build an usages string slice from the Secret data
	var usages []string
	for k, v := range secret.Data {
		// Skip all fields that don't include this prefix
		if !strings.HasPrefix(k, bootstrapapi.BootstrapTokenUsagePrefix) {
			continue
		}
		// Skip those that don't have this usage set to true
		if string(v) != "true" {
			continue
		}
		usages = append(usages, strings.TrimPrefix(k, bootstrapapi.BootstrapTokenUsagePrefix))
	}
	// Only sort the slice if defined
	if usages != nil {
		sort.Strings(usages)
	}

	// Get the extra groups information from the Secret
	// It's done this way to make .Groups be nil in case there is no items, rather than an
	// empty slice or an empty slice with a "" string only
	var groups []string
	groupsString := getSecretString(secret, bootstrapapi.BootstrapTokenExtraGroupsKey)
	g := strings.Split(groupsString, ",")
	if len(g) > 0 && len(g[0]) > 0 {
		groups = g
	}

	return &BootstrapToken{
		Token:       bts,
		Description: description,
		Expires:     expires,
		Usages:      usages,
		Groups:      groups,
	}, nil
}

// getSecretString returns the string value for the given key in the specified Secret
func getSecretString(secret *v1.Secret, key string) string {
	if secret.Data == nil {
		return ""
	}
	if val, ok := secret.Data[key]; ok {
		return string(val)
	}
	return ""
}

// NewBootstrapTokenStringFromIDAndSecret is a wrapper around NewBootstrapTokenString
// that allows the caller to specify the ID and Secret separately
func newBootstrapTokenStringFromIDAndSecret(id, secret string) (*BootstrapTokenString, error) {
	return newBootstrapTokenString(bootstraputil.TokenFromIDAndSecret(id, secret))
}
