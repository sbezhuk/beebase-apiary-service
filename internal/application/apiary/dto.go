package apiary

// CreateInput is the input to Service.Create.
type CreateInput struct {
	Name        string
	Location    string
	Description string
	Lat         *float64
	Lon         *float64
}

// UpdateInput is the input to Service.Update. Update replaces all fields
// (PUT semantics), not a partial patch.
type UpdateInput struct {
	Name        string
	Location    string
	Description string
	Lat         *float64
	Lon         *float64
}
