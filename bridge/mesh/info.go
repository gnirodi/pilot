package mesh

import (
	"os"
	"sync"
)

const (
	EnvVarNodeName          = "MY_NODE_NAME"
	EnvVarPodName           = "MY_POD_NAME"
	EnvVarPodNamespace      = "MY_POD_NAMESPACE"
	EnvVarPodIP             = "MY_POD_IP"
	EnvVarPodServiceAccount = "MY_POD_SERVICE_ACCOUNT"
	ServerStatus            = "MY_SERVER_STATUS"
	StatusHeader            = "STATUS_HEADER"
	ServerStatusHeader      = "Istio Hybrid Multi-Zone Mesh Status"
)

type MeshInfo struct {
	labels map[string]string
	mu     sync.RWMutex
}

func NewMeshInfo() *MeshInfo {
	pi := MeshInfo{map[string]string{}, sync.RWMutex{}}
	return &pi
}

func (m *MeshInfo) BuildStatus(statusHeader string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.labels[StatusHeader] = statusHeader
	m.labels[EnvVarNodeName] = os.Getenv(EnvVarNodeName)
	m.labels[EnvVarPodName] = os.Getenv(EnvVarPodName)
	m.labels[EnvVarPodNamespace] = os.Getenv(EnvVarPodNamespace)
	m.labels[EnvVarPodIP] = os.Getenv(EnvVarPodIP)
	m.labels[EnvVarPodServiceAccount] = os.Getenv(EnvVarPodServiceAccount)
	m.labels[ServerStatus] = "Initializing"
}

func (m MeshInfo) SetHealth(newStatus string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.labels[ServerStatus] = newStatus
}

func (m MeshInfo) GetStatusHeader() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[StatusHeader]
}

func (m MeshInfo) GetNodeName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[EnvVarNodeName]
}

func (m MeshInfo) GetPodName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[EnvVarPodName]
}

func (m MeshInfo) GetPodNamespace() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[EnvVarPodNamespace]
}

func (m MeshInfo) GetPodIP() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[EnvVarPodIP]
}

func (m MeshInfo) GetServerStatus() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.labels[ServerStatus]
}
