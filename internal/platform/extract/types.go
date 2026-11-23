// Package extract reads values out of HTML and JSON answers by selector, filters and templates; the declarative engines share it.
package extract

import "encoding/json"

// Extractor reads one value: by selector, attribute and filters, or as a template (Text), or by the first matching case.
type Extractor struct {
	Name      string   `json:"name"`
	Selector  string   `json:"selector"`
	Attribute string   `json:"attribute"`
	Text      *string  `json:"text"`
	Default   *string  `json:"default"`
	Remove    string   `json:"remove"`
	Optional  bool     `json:"optional"`
	Filters   []Filter `json:"filters"`
	Case      []Case   `json:"case"`
}

// Case is a value taken when its selector matches.
type Case struct {
	Selector string `json:"selector"`
	Value    string `json:"value"`
}

// Filter is a step applied to an extracted value.
type Filter struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}

// UnmarshalJSON accepts a lone string as the arguments, which the converted data keeps for date headings.
func (f *Filter) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.Name, f.Args = raw.Name, nil
	if len(raw.Args) == 0 || string(raw.Args) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw.Args, &f.Args); err != nil {
		var one string
		if err := json.Unmarshal(raw.Args, &one); err != nil {
			return err
		}
		f.Args = []string{one}
	}
	return nil
}
