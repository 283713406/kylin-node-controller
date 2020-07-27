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

package ping

import (
	"time"

	"github.com/kylin/kylin-node-controller/cmd/constants"
	p "github.com/sparrc/go-ping"
)

func AddrAccessible(ip string) bool {
	pinger, err := p.NewPinger(ip)
	if err != nil {
		panic(err)
	}

	pinger.Count = constants.ICMPCOUNT
	pinger.Timeout = time.Duration(constants.PINGTIME * time.Millisecond)
	pinger.SetPrivileged(true)
	pinger.Run()

	stats := pinger.Statistics()

	if stats.PacketsRecv >= 1 {
		return true
	}

	return false
}
