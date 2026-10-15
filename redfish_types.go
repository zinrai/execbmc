package main

type odataRef struct {
	ODataID string `json:"@odata.id"`
}

type serviceRoot struct {
	ODataID        string   `json:"@odata.id"`
	ODataType      string   `json:"@odata.type"`
	ID             string   `json:"Id"`
	Name           string   `json:"Name"`
	RedfishVersion string   `json:"RedfishVersion"`
	Systems        odataRef `json:"Systems"`
}

type systemCollection struct {
	ODataID      string     `json:"@odata.id"`
	ODataType    string     `json:"@odata.type"`
	Name         string     `json:"Name"`
	MembersCount int        `json:"Members@odata.count"`
	Members      []odataRef `json:"Members"`
}

type systemBoot struct {
	BootSourceOverrideTarget          string   `json:"BootSourceOverrideTarget"`
	BootSourceOverrideEnabled         string   `json:"BootSourceOverrideEnabled"`
	BootSourceOverrideTargetAllowable []string `json:"BootSourceOverrideTarget@Redfish.AllowableValues"`
}

type systemActions struct {
	Reset systemResetAction `json:"#ComputerSystem.Reset"`
}

type systemResetAction struct {
	Target             string   `json:"target"`
	ResetTypeAllowable []string `json:"ResetType@Redfish.AllowableValues"`
}

type computerSystem struct {
	ODataID    string        `json:"@odata.id"`
	ODataType  string        `json:"@odata.type"`
	ID         string        `json:"Id"`
	Name       string        `json:"Name"`
	SystemType string        `json:"SystemType"`
	PowerState string        `json:"PowerState"`
	Boot       systemBoot    `json:"Boot"`
	Actions    systemActions `json:"Actions"`
}

type systemPatch struct {
	Boot *bootPatch `json:"Boot"`
}

type bootPatch struct {
	BootSourceOverrideTarget  *string `json:"BootSourceOverrideTarget"`
	BootSourceOverrideEnabled *string `json:"BootSourceOverrideEnabled"`
}

type resetRequest struct {
	ResetType string `json:"ResetType"`
}

type redfishError struct {
	Error redfishErrorBody `json:"error"`
}

type redfishErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
