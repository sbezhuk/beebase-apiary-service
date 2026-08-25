package apiary

// CreateInput is the input to Service.Create.
type CreateInput struct {
	Name     string
	Location string
	Notes    string
}

// UpdateInput is the input to Service.Update. Update replaces all three
// fields (PUT semantics), not a partial patch.
type UpdateInput struct {
	Name     string
	Location string
	Notes    string
}
