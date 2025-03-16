package models

type MBSItem struct {
	Anaes                bool    `json:"Anaes"`
	AnaesChange          bool    `json:"AnaesChange"`
	BasicUnits           int     `json:"BasicUnits"`
	Benefit100           float64 `json:"Benefit100"`
	Benefit75            float64 `json:"Benefit75"`
	Benefit85            float64 `json:"Benefit85"`
	BenefitChange        bool    `json:"BenefitChange"`
	BenefitStartDate     string  `json:"BenefitStartDate"`
	BenefitType          string  `json:"BenefitType"`
	Category             string  `json:"Category"`
	DerivedFee           float64 `json:"DerivedFee"`
	DerivedFeeStartDate  string  `json:"DerivedFeeStartDate"`
	Description          string  `json:"Description"`
	DescriptionStartDate string  `json:"DescriptionStartDate"`
	DescriptorChange     bool    `json:"DescriptorChange"`
	EMSNCap              float64 `json:"EMSNCap"`
	EMSNChange           bool    `json:"EMSNChange"`
	EMSNChangeDate       string  `json:"EMSNChangeDate"`
	EMSNDescription      string  `json:"EMSNDescription"`
	EMSNEndDate          string  `json:"EMSNEndDate"`
	EMSNFixedCapAmount   float64 `json:"EMSNFixedCapAmount"`
	EMSNMaximumCap       float64 `json:"EMSNMaximumCap"`
	EMSNPercentageCap    float64 `json:"EMSNPercentageCap"`
	EMSNStartDate        string  `json:"EMSNStartDate"`
	FeeChange            bool    `json:"FeeChange"`
	FeeStartDate         string  `json:"FeeStartDate"`
	FeeType              string  `json:"FeeType"`
	Group                string  `json:"Group"`
	ItemChange           bool    `json:"ItemChange"`
	ItemEndDate          string  `json:"ItemEndDate"`
	ItemNum              string  `json:"ItemNum"`
	ItemStartDate        string  `json:"ItemStartDate"`
	ItemType             string  `json:"ItemType"`
	NewItem              bool    `json:"NewItem"`
	ProviderType         string  `json:"ProviderType"`
	QFEEndDate           string  `json:"QFEEndDate"`
	QFEStartDate         string  `json:"QFEStartDate"`
	ScheduleFee          float64 `json:"ScheduleFee"`
	SubGroup             string  `json:"SubGroup"`
	SubHeading           string  `json:"SubHeading"`
	SubItemNum           string  `json:"SubItemNum"`
}

type Config struct {
	QdrantHost   string
	QdrantPort   int
	NumWorkers   int
	APIKey       string
	ServerPort   int
	ServerAPIKey string
}

type ProcessResponse struct {
	ItemsProcessed int    `json:"items_processed"`
	ItemsSkipped   int    `json:"items_skipped"`
	ItemsUpdated   int    `json:"items_updated"`
	ItemsRemoved   int    `json:"items_removed"`
	Error          string `json:"error,omitempty"`
}

type EmbeddingJob struct {
	ItemNum string
	Text    string
	Item    MBSItem
	NewHash string
}

type EmbeddingResult struct {
	ItemNum string
	Vector  []float32
	Item    MBSItem
	NewHash string
	Error   error
}
