package core

const NormVersion = 1

const PrefixLen = 4

type BucketRequest struct {
	Prefix      string `json:"prefix"`
	NormVersion int    `json:"norm_version"`
}

type BucketEntry struct {
	Hash    string   `json:"hash"`
	Verdict Verdict  `json:"verdict"`
	Reasons []string `json:"reasons,omitempty"`
	Source  string   `json:"source"`
	Votes   int      `json:"votes"`
}

type BucketResponse struct {
	Prefix      string        `json:"prefix"`
	NormVersion int           `json:"norm_version"`
	Entries     []BucketEntry `json:"entries"`
}

type SubmitEntry struct {
	Hash    string   `json:"hash"`
	Verdict Verdict  `json:"verdict"`
	Reasons []string `json:"reasons,omitempty"`
	Source  string   `json:"source"`
}

type SubmitRequest struct {
	ClientID    string        `json:"client_id"`
	NormVersion int           `json:"norm_version"`
	Entries     []SubmitEntry `json:"entries"`
}

type SubmitResponse struct {
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
}

type VoteRequest struct {
	ClientID    string  `json:"client_id"`
	NormVersion int     `json:"norm_version"`
	Hash        string  `json:"hash"`
	Verdict     Verdict `json:"verdict"`
}

type VoteResponse struct {
	Hash  string `json:"hash"`
	Votes int    `json:"votes"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

const MinBucket = 8
