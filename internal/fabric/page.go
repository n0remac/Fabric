package fabric

const ProtocolVersion = "0.1"

type Page struct {
	Fabric string    `json:"fabric"`
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Layout Component `json:"layout"`
	Data   *DataSpec `json:"data,omitempty"`
}

type DataSpec struct {
	Provider string      `json:"provider"`
	Refresh  RefreshSpec `json:"refresh"`
}

type RefreshStrategy string

const (
	RefreshManual   RefreshStrategy = "manual"
	RefreshOnOpen   RefreshStrategy = "on-open"
	RefreshInterval RefreshStrategy = "interval"
)

type RefreshSpec struct {
	Strategy RefreshStrategy `json:"strategy"`
	Seconds  int             `json:"seconds,omitempty"`
}
