package render

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	explain "github.com/soroauth/soroauth-explain"
)

func TestJSONRoundTrip(t *testing.T) {
	exp := sample()
	out, err := JSON(exp)
	if err != nil {
		t.Fatal(err)
	}
	var back explain.Explanation
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, exp) {
		t.Fatalf("round trip changed the explanation\n%s", out)
	}
	if !bytes.Contains(out, []byte(`"nonce": -9223372036854775808`)) {
		t.Fatalf("int64 nonce not exact:\n%s", out)
	}
	if !bytes.HasSuffix(out, []byte("}\n")) {
		t.Fatal("output does not end in a newline")
	}
}

// TestJSONSortedKeys checks every object in the output has its keys in
// sorted order.
func TestJSONSortedKeys(t *testing.T) {
	out, err := JSON(sample())
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	type frame struct {
		object bool
		keys   []string
		expect bool // next string token in an object is a key
	}
	var stack []*frame
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		var top *frame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				if top != nil && top.object {
					top.expect = true
				}
				stack = append(stack, &frame{object: v == '{', expect: v == '{'})
			case '}', ']':
				f := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				for i := 1; i < len(f.keys); i++ {
					if f.keys[i-1] >= f.keys[i] {
						t.Errorf("keys out of order: %q before %q", f.keys[i-1], f.keys[i])
					}
				}
			}
		default:
			if top != nil && top.object {
				if top.expect {
					top.keys = append(top.keys, v.(string))
					top.expect = false
				} else {
					top.expect = true
				}
			}
		}
	}
	if len(stack) != 0 {
		t.Fatal("unbalanced output")
	}
}
