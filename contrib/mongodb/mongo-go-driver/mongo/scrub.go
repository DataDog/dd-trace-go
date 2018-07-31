package mongo

import "github.com/mongodb/mongo-go-driver/bson"

func scrub(doc *bson.Document) *bson.Document {
	scrubbed := bson.NewDocument()
	it := doc.Iterator()
	for it.Next() {
		el := it.Element()
		if contains(unscrubbedFields, el.Key()) && el.Value().Type() == bson.TypeString {
			scrubbed.Append(el)
		} else {
			child := scrubValue(el.Value())
			scrubbed.Append(bson.EC.Interface(el.Key(), child))
		}
	}
	return scrubbed
}

func scrubArray(arr *bson.Array) *bson.Array {
	scrubbed := bson.NewArray()
	if it, err := arr.Iterator(); err == nil {
		for it.Next() {
			scrubbed.Append(scrubValue(it.Value()))
		}
	}
	return scrubbed
}

func scrubValue(val *bson.Value) *bson.Value {
	scrubbed := bson.VC.Null()
	switch val.Type() {
	case bson.TypeEmbeddedDocument:
		scrubbed = bson.VC.Document(scrub(val.MutableDocument()))
	case bson.TypeArray:
		scrubbed = bson.VC.Array(scrubArray(val.MutableArray()))
	default:
		scrubbed = bson.VC.String("?")
	}
	return scrubbed
}

func contains(haystack []string, needle string) bool {
	for _, el := range haystack {
		if needle == el {
			return true
		}
	}
	return false
}
