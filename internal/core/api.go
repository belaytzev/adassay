package core

// NormVersion is the version of the segment normalization algorithm. Any change
// to normalization must bump it: hashes made by different versions must never
// share a bucket, otherwise the shared database silently rots.
const NormVersion = 1

// PrefixLen is the number of leading hex characters of a segment hash the
// client is allowed to send. 4 hex chars = 16 bits = 65536 buckets.
// ponytail: fixed prefix, make it adaptive if bucket sizes skew
const PrefixLen = 4

// BucketRequest is GET /v1/segments/{prefix}, norm_version as a query parameter.
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

// SubmitRequest is POST /v1/segments.
type SubmitRequest struct {
	ClientID    string        `json:"client_id"`
	NormVersion int           `json:"norm_version"`
	Entries     []SubmitEntry `json:"entries"`
}

type SubmitResponse struct {
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
}

// VoteRequest is POST /v1/vote.
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

// MinBucket is the number of entries a bucket response must contain. A bucket
// of one is not anonymity: it tells the server exactly which segment was
// looked up. Short buckets are padded to this size, and a client that receives
// fewer entries is talking to a server that does not honour the contract.
const MinBucket = 8
