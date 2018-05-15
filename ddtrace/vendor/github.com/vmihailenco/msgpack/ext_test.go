package msgpack_test

import (
	"reflect"
	"testing"

	"github.com/vmihailenco/msgpack"
	"github.com/vmihailenco/msgpack/codes"
)

func init() {
	msgpack.RegisterExt(9, (*ExtTest)(nil))
}

func TestRegisterExtPanic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("panic expected")
		}
		got := r.(error).Error()
		wanted := "msgpack: ext with id=9 is already registered"
		if got != wanted {
			t.Fatalf("got %q, wanted %q", got, wanted)
		}
	}()
	msgpack.RegisterExt(9, (*ExtTest)(nil))
}

type ExtTest struct {
	S string
}

var _ msgpack.CustomEncoder = (*ExtTest)(nil)
var _ msgpack.CustomDecoder = (*ExtTest)(nil)

func (ext ExtTest) EncodeMsgpack(e *msgpack.Encoder) error {
	return e.EncodeString("hello " + ext.S)
}

func (ext *ExtTest) DecodeMsgpack(d *msgpack.Decoder) error {
	var err error
	ext.S, err = d.DecodeString()
	return err
}

func TestExt(t *testing.T) {
	for _, v := range []interface{}{ExtTest{"world"}, &ExtTest{"world"}} {
		b, err := msgpack.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}

		var dst interface{}
		err = msgpack.Unmarshal(b, &dst)
		if err != nil {
			t.Fatal(err)
		}

		v, ok := dst.(*ExtTest)
		if !ok {
			t.Fatalf("got %#v, wanted ExtTest", dst)
		}

		wanted := "hello world"
		if v.S != wanted {
			t.Fatalf("got %q, wanted %q", v.S, wanted)
		}

		ext := new(ExtTest)
		err = msgpack.Unmarshal(b, ext)
		if err != nil {
			t.Fatal(err)
		}
		if ext.S != wanted {
			t.Fatalf("got %q, wanted %q", ext.S, wanted)
		}
	}
}

func TestUnknownExt(t *testing.T) {
	b := []byte{byte(codes.FixExt1), 1, 0}

	var dst interface{}
	err := msgpack.Unmarshal(b, &dst)
	if err == nil {
		t.Fatalf("got nil, wanted error")
	}
	got := err.Error()
	wanted := "msgpack: unregistered ext id=1"
	if got != wanted {
		t.Fatalf("got %q, wanted %q", got, wanted)
	}
}

func TestDecodeExtWithMap(t *testing.T) {
	type S struct {
		I int
	}
	msgpack.RegisterExt(2, S{})

	b, err := msgpack.Marshal(&S{I: 42})
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]interface{}
	if err := msgpack.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}

	wanted := map[string]interface{}{"I": int8(42)}
	if !reflect.DeepEqual(got, wanted) {
		t.Fatalf("got %#v, but wanted %#v", got, wanted)
	}
}
