// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"net"
	"slices"
	"strconv"

	"github.com/nstance-dev/nstance/pkg/proxy"
)

// ProxyConfig derives provider-neutral listeners from load balancers and group references.
func (c *Config) ProxyConfig() (proxy.Config, error) {
	refs := make(map[string]map[string][]string)
	for tenant, groups := range c.Groups {
		for groupName, group := range groups {
			for _, lbName := range group.LoadBalancers {
				if _, ok := c.LoadBalancers[lbName]; !ok {
					return proxy.Config{}, fmt.Errorf("group %s in tenant %s references unknown load balancer: %s", groupName, tenant, lbName)
				}
				if refs[lbName] == nil {
					refs[lbName] = make(map[string][]string)
				}
				if !slices.Contains(refs[lbName][tenant], groupName) {
					refs[lbName][tenant] = append(refs[lbName][tenant], groupName)
				}
			}
		}
	}

	serverPorts := make(map[int]bool)
	for _, addr := range []string{c.Shard.Bind.RegistrationAddr, c.Shard.Bind.AgentAddr, c.Shard.Bind.OperatorAddr, c.Shard.Bind.HealthAddr, c.Shard.Bind.ElectionAddr} {
		_, value, err := net.SplitHostPort(addr)
		if err == nil {
			port, _ := strconv.Atoi(value)
			serverPorts[port] = true
		}
	}

	result := proxy.Config{Listeners: make(map[string]proxy.Listener)}
	type portUse struct {
		exclusive string
		google    []string
	}
	usedPorts := make(map[int]*portUse)
	googleSelectors := make(map[string]string)
	googlePorts := make(map[int]int)
	for _, lb := range c.LoadBalancers {
		if lb.Provider == "google" {
			for _, frontend := range lb.Frontends {
				googlePorts[frontend.Port]++
			}
		}
	}
	for lbName, lb := range c.LoadBalancers {
		tenants := refs[lbName]
		if len(tenants) == 0 {
			return proxy.Config{}, fmt.Errorf("load balancer %s is not referenced by any group", lbName)
		}
		if len(tenants) != 1 {
			return proxy.Config{}, fmt.Errorf("load balancer %s is referenced across tenants", lbName)
		}
		var tenant string
		var groups []string
		for tenant, groups = range tenants {
			slices.Sort(groups)
		}
		add := func(key string, listener proxy.Listener, exclusive bool) error {
			if serverPorts[listener.ProxyPort] {
				return fmt.Errorf("proxy listener %s collides with nstance-server port %d", key, listener.ProxyPort)
			}
			use := usedPorts[listener.ProxyPort]
			if use == nil {
				use = &portUse{}
				usedPorts[listener.ProxyPort] = use
			}
			if exclusive {
				if use.exclusive != "" {
					return fmt.Errorf("proxy port %d collision between %s and %s", listener.ProxyPort, use.exclusive, key)
				}
				if len(use.google) > 0 {
					return fmt.Errorf("proxy port %d collision between Google listener %s and exclusive listener %s", listener.ProxyPort, use.google[0], key)
				}
				use.exclusive = key
			} else {
				if use.exclusive != "" {
					return fmt.Errorf("proxy port %d collision between exclusive listener %s and Google listener %s", listener.ProxyPort, use.exclusive, key)
				}
				selector := net.JoinHostPort(listener.DestinationIP, strconv.Itoa(listener.ProxyPort))
				if previous := googleSelectors[selector]; previous != "" {
					return fmt.Errorf("google listener selector %s collision between %s and %s", selector, previous, key)
				}
				googleSelectors[selector] = key
				use.google = append(use.google, key)
			}
			if _, exists := result.Listeners[key]; exists {
				return fmt.Errorf("listener identity collision: %s", key)
			}
			result.Listeners[key] = listener
			return nil
		}
		switch lb.Provider {
		case "aws":
			for _, item := range lb.TargetGroups {
				key := fmt.Sprintf("%s:%d", lbName, item.ProxyPort)
				if err := add(key, proxy.Listener{Tenant: tenant, Groups: groups, TargetPort: item.TargetPort, ProxyPort: item.ProxyPort}, true); err != nil {
					return proxy.Config{}, err
				}
			}
		case "tunnel":
			for _, item := range lb.Listeners {
				key := fmt.Sprintf("%s:%d", lbName, item.ProxyPort)
				if err := add(key, proxy.Listener{Tenant: tenant, Groups: groups, TargetPort: item.TargetPort, ProxyPort: item.ProxyPort}, true); err != nil {
					return proxy.Config{}, err
				}
			}
		case "google":
			for _, item := range lb.Frontends {
				listener := proxy.Listener{Tenant: tenant, Groups: groups, TargetPort: item.Port, ProxyPort: item.Port}
				key := fmt.Sprintf("%s:%d", lbName, item.Port)
				if googlePorts[item.Port] > 1 {
					listener.DestinationIP = item.IP
					key = lbName + "/" + net.JoinHostPort(item.IP, strconv.Itoa(item.Port))
				}
				if err := add(key, listener, false); err != nil {
					return proxy.Config{}, err
				}
			}
		}
	}
	return result, nil
}
