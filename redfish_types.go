package main

// odataRef is a link to another resource.
type odataRef struct {
	ODataID string `json:"@odata.id"`
}

// serviceRoot is the response for GET /redfish/v1/.
type serviceRoot struct {
	ODataID        string   `json:"@odata.id"`
	ODataType      string   `json:"@odata.type"`
	ID             string   `json:"Id"`
	Name           string   `json:"Name"`
	RedfishVersion string   `json:"RedfishVersion"`
	Systems        odataRef `json:"Systems"`
}

// systemCollection is the response for GET /redfish/v1/Systems.
type systemCollection struct {
	ODataID      string     `json:"@odata.id"`
	ODataType    string     `json:"@odata.type"`
	Name         string     `json:"Name"`
	MembersCount int        `json:"Members@odata.count"`
	Members      []odataRef `json:"Members"`
}

// systemBoot is the Boot object inside a ComputerSystem.
type systemBoot struct {
	BootSourceOverrideTarget          string   `json:"BootSourceOverrideTarget"`
	BootSourceOverrideEnabled         string   `json:"BootSourceOverrideEnabled"`
	BootSourceOverrideTargetAllowable []string `json:"BootSourceOverrideTarget@Redfish.AllowableValues"`
}

// systemActions advertises the Reset action.
type systemActions struct {
	Reset systemResetAction `json:"#ComputerSystem.Reset"`
}

type systemResetAction struct {
	Target             string   `json:"target"`
	ResetTypeAllowable []string `json:"ResetType@Redfish.AllowableValues"`
}

// computerSystem is the response for GET /redfish/v1/Systems/1.
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

// systemPatch is the accepted body for PATCH /redfish/v1/Systems/1.
type systemPatch struct {
	Boot *struct {
		BootSourceOverrideTarget  *string `json:"BootSourceOverrideTarget"`
		BootSourceOverrideEnabled *string `json:"BootSourceOverrideEnabled"`
	} `json:"Boot"`
}

// resetRequest is the body for POST .../Actions/ComputerSystem.Reset.
type resetRequest struct {
	ResetType string `json:"ResetType"`
}

// redfishError is a minimal Redfish error response.
type redfishError struct {
	Error redfishErrorBody `json:"error"`
}

type redfishErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
