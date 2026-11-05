package project

// ID and ProfileID prevent crossing project and profile identities at use-case boundaries.
type ID string
type ProfileID string

// Ref fixes a saved configuration revision for an operation.
type Ref struct {
	ProjectID        ID        `json:"projectId"`
	ProfileID        ProfileID `json:"profileId"`
	ExpectedRevision int32     `json:"expectedRevision"`
}
