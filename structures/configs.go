package structures

type NodeLevelConfig struct {
	PublicKey                    string            `json:"PUBLIC_KEY"`
	PrivateKey                   string            `json:"PRIVATE_KEY"`
	PointOfDistributionWS        string            `json:"POINT_OF_DISTRIBUTION_WS"`
	AnchorsPointOfDistributionWS string            `json:"ANCHORS_POINT_OF_DISTRIBUTION_WS"`
	DisablePoDOutbox             bool              `json:"DISABLE_POD_OUTBOX"`
	ExtraDataToBlock             map[string]string `json:"EXTRA_DATA_TO_BLOCK"`
	TxsMempoolSize               int               `json:"TXS_MEMPOOL_SIZE"`
	BootstrapNodes               []string          `json:"BOOTSTRAP_NODES"`
	MyHostname                   string            `json:"MY_HOSTNAME"`
	Interface                    string            `json:"INTERFACE"`
	Port                         int               `json:"PORT"`
	WebSocketInterface           string            `json:"WEBSOCKET_INTERFACE"`
	WebSocketPort                int               `json:"WEBSOCKET_PORT"`
}
