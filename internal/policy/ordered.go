package policy

import (
	"bytes"
	"encoding/json"
)

// Ordered is a string map that keeps insertion order when encoded as a JSON object.
// Jev reads criteria in the order they are written, so option order stays stable across steps.
type Ordered struct {
	keys   []string
	values map[string]string
}

func (o *Ordered) Set(key, value string) {
	if o.values == nil {
		o.values = map[string]string{}
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o Ordered) Get(key string) (string, bool) {
	v, ok := o.values[key]
	return v, ok
}

func (o Ordered) Keys() []string { return append([]string(nil), o.keys...) }

func (o Ordered) Len() int { return len(o.keys) }

func (o Ordered) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(o.values[k])
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
