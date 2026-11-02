// Importing an existing container as an Orbit service.
package protocol

const ActionContainerInspect = "container.inspect" // ContainerArgs; returns ImportSpec

func init() { Actions[ActionContainerInspect] = true }

// ImportSpec is what a deploy needs to recreate an unmanaged container as an Orbit service.
type ImportSpec struct {
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Ports       []string          `json:"ports"`
	Env         map[string]string `json:"env"`     // only what the container adds to the image's
	Command     []string          `json:"command"` // only when it differs from the image's
	Carry       Carried           `json:"carry"`
	Compose     string            `json:"compose,omitempty"`     // Compose project, if any
	Unsupported []string          `json:"unsupported,omitempty"` // settings Orbit can't recreate, in plain words
}

// Carried settings come from the imported container and are never editable afterwards: host bind mounts
// would otherwise let anyone who can edit a service mount the server's filesystem.
type Carried struct {
	Mounts   []Mount           `json:"mounts,omitempty"`
	Networks []NetworkAttach   `json:"networks,omitempty"` // joined besides the orbit network, e.g. a Compose project's
	HostIPs  map[string]string `json:"host_ips,omitempty"` // "5432/tcp" → "127.0.0.1": ports published on one address only
	Labels   map[string]string `json:"labels,omitempty"`
}

type Mount struct {
	Type     string `json:"type"` // bind | volume | tmpfs
	Source   string `json:"source,omitempty"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

type NetworkAttach struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
}
