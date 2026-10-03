// Package kube is the names Kubernetes and the operators on it give things,
// which this estate reads and does not choose. Each is here once: the program
// that retires a machine and the tests that check where work is placed ask
// the cluster the same question, and have to spell it the same way.
package kube

// ControlPlaneLabel is on every control-plane node, with no value.
const ControlPlaneLabel = "node-role.kubernetes.io/control-plane"

// DatabaseLabel is on every pod and volume claim a CloudNativePG cluster
// owns; its value is the cluster's name.
const DatabaseLabel = "cnpg.io/cluster"
