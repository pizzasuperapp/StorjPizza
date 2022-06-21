// Copyright (C) 2020 Storj Labs, Inc.
// See LICENSE for copying information.

package uploadselection

import (
	mathrand "math/rand" // Using mathrand here because crypto-graphic randomness is not required and simplifies code.
)

// SelectByID implements selection from nodes with every node having equal probability.
type SelectByID []*Node

var _ Selector = (SelectByID)(nil)

// Count returns the number of maximum number of nodes that it can return.
func (nodes SelectByID) Count() int { return len(nodes) }

// Select selects upto n nodes.
func (nodes SelectByID) Select(n int, criteria Criteria) []*Node {
	if n <= 0 {
		return nil
	}

	selected := []*Node{}
	for _, idx := range mathrand.Perm(len(nodes)) {
		node := nodes[idx]

		if !criteria.MatchInclude(node) {
			continue
		}

		selected = append(selected, node.Clone())
		if len(selected) >= n {
			break
		}
	}

	return selected
}

// SelectBySubnet implements selection from nodes with every subnet having equal probability.
type SelectBySubnet []Subnet

var _ Selector = (SelectBySubnet)(nil)

// Subnet groups together nodes with the same subnet.
type Subnet struct {
	Net   string
	Nodes []*Node
}

// SelectBySubnetFromNodes creates SelectBySubnet selector from nodes.
func SelectBySubnetFromNodes(nodes []*Node) SelectBySubnet {
	bynet := map[string][]*Node{}
	for _, node := range nodes {
		bynet[node.LastNet] = append(bynet[node.LastNet], node)
	}

	var subnets SelectBySubnet
	for net, nodes := range bynet {
		subnets = append(subnets, Subnet{
			Net:   net,
			Nodes: nodes,
		})
	}

	return subnets
}

// Count returns the number of maximum number of nodes that it can return.
func (subnets SelectBySubnet) Count() int { return len(subnets) }

// Select selects upto n nodes.
func (subnets SelectBySubnet) Select(n int, criteria Criteria) []*Node {
	if n <= 0 {
		return nil
	}

	selected := []*Node{}
	for _, idx := range mathrand.Perm(len(subnets)) {
		subnet := subnets[idx]
		node := subnet.Nodes[mathrand.Intn(len(subnet.Nodes))]

		if !criteria.MatchInclude(node) {
			continue
		}

		selected = append(selected, node.Clone())
		if len(selected) >= n {
			break
		}
	}

	return selected
}
