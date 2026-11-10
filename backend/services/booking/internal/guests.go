package booking

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Adults are a category, not a request for their exact age. Child age selects the tariff.
// The age-only input remains supported for compatibility with earlier local clients.
func (g *Guest) UnmarshalJSON(data []byte) error {
	var in struct {
		Category string `json:"category"`
		Age      *int   `json:"age"`
		Count    int    `json:"count"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		return err
	}
	g.Count = in.Count
	switch in.Category {
	case "adult":
		if in.Age != nil {
			return errors.New("adult category does not require an age")
		}
		g.Age = 18
	case "child":
		if in.Age == nil || *in.Age < 0 || *in.Age > 17 {
			return errors.New("child age must be 0..17")
		}
		g.Age = *in.Age
	case "":
		if in.Age == nil {
			return errors.New("guest category is required")
		}
		g.Age = *in.Age
	default:
		return errors.New("unknown guest category")
	}
	return nil
}

func (g Guest) MarshalJSON() ([]byte, error) {
	type wireGuest struct {
		Category string `json:"category"`
		Count    int    `json:"count"`
		Age      *int   `json:"age,omitempty"`
	}
	w := wireGuest{Category: "adult", Count: g.Count}
	if g.Age < 18 {
		w.Category = "child"
		age := g.Age
		w.Age = &age
	}
	return json.Marshal(w)
}
