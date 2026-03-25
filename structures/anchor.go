package structures

type Anchor struct {
	Pubkey       string `json:"pubkey"`
	AnchorUrl    string `json:"anchorURL"`
	WssAnchorUrl string `json:"wssAnchorURL"`
}
