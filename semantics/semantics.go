package semantics

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed schemav1.yaml
var rawSchema []byte

type rules struct {
	Semantics []Semantic `yaml:"semantics"`
}

type Semantic struct {
	ID          uint64 `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	IsSensitive bool   `yaml:"is_sensitive"` //todo: should this allow a "maybe"?
}

var semantics map[string]Semantic

func init() {
	var err error
	semantics, err = load(rawSchema)
	if err != nil {
		panic("Embedded semantics file was not valid!")
	}
	rawSchema = nil //don't keep this memory around
}

func load(in []byte) (map[string]Semantic, error) {
	rs := rules{}
	byName := map[string]Semantic{}
	err := yaml.Unmarshal(in, &rs)
	if err != nil {
		return byName, err
	}
	for _, r := range rs.Semantics {
		byName[r.Name] = r
	}
	return byName, nil
}

// Get returns the Semantic definition for a given name, nil if none is found
// Do not modify the returned semantic value
func Get(name string) *Semantic {
	if s, ok := semantics[name]; ok {
		return &s
	}
	return nil
}
