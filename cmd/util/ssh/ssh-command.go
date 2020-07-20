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

package ssh

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/kylin/kylin-node-controller/cmd/constants"
	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
)

func connect(user, password, host string, port int) (*ssh.Session, error) {
	var (
		auth  []ssh.AuthMethod
		addr  string
		clientConfig *ssh.ClientConfig
		client *ssh.Client
		session *ssh.Session
		err  error
	)

	// get auth method
	auth = make([]ssh.AuthMethod, 0)
	auth = append(auth, ssh.Password(password))

	clientConfig = &ssh.ClientConfig{
		User: user,
		Auth: auth,
		Timeout: 30 * time.Second,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
	}

	// connet to ssh
	addr = fmt.Sprintf("%s:%d", host, port)

	if client, err = ssh.Dial("tcp", addr, clientConfig); err != nil {
		return nil, errors.Wrap(err, "failed to starts a client connection to the given SSH server")
	}

	// create session
	if session, err = client.NewSession(); err != nil {
		return nil, errors.Wrap(err, "failed to opens a new Session for ssh client")
	}

	return session, nil
}

func RunSshCommand(userName, password, host, cmd string) error {
	session, err := connect(userName, password, host, constants.SshPort)
	if err != nil {
		return errors.Wrap(err, "failed to connect ssh client")
	}
	defer session.Close()

	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	// cmd := "kubeadm join 10.9.8.134:6443 --token dggd25.ix1rs6yhqcatorpm --discovery-token-ca-cert-hash /
	// sha256:71bc95b5777a3a175d9b8787b43e97613325e5e91250bac62031bbbd7811c0a0"
	if err = session.Run(cmd); err != nil{
		return errors.Wrap(err, "failed to run cmd for ssh client")
	}

	return nil
}