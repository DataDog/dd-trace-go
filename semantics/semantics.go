package semantics

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed schemav1.yaml
var rawSchema []byte

type rules struct {
	Version   string     `yaml:"version"`
	Semantics []Semantic `yaml:"semantics"`
}

type Semantic struct {
	ID          SemanticID `yaml:"id"`
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	IsSensitive bool       `yaml:"is_sensitive"` //todo: should this allow a "maybe"?
}

var semantics map[SemanticID]Semantic

type SemanticID int

var Version string

// TODO: use code gen to build this from the schema
const (
	HTTP_URL            SemanticID = 1
	SQL_QUERY                      = 2
	REDIS_COMMAND                  = 3
	MEMCACHED_COMMAND              = 4
	MONGODB_QUERY                  = 5
	ELASTICSEARCH_QUERY            = 6
)

func init() {
	var err error
	semantics, err = load(rawSchema)
	if err != nil {
		panic("Embedded semantics file was not valid!")
	}
	rawSchema = nil //don't keep this memory around
}

func load(in []byte) (map[SemanticID]Semantic, error) {
	rs := rules{}
	byID := map[SemanticID]Semantic{}
	err := yaml.Unmarshal(in, &rs)
	if err != nil {
		return byID, err
	}
	for _, r := range rs.Semantics {
		byID[r.ID] = r
	}
	Version = rs.Version
	return byID, nil
}

// Get returns the Semantic definition for a given name, nil if none is found
// Do not modify the returned semantic value
func Get(id SemanticID) *Semantic {
	if s, ok := semantics[id]; ok {
		return &s
	}
	return nil
}
