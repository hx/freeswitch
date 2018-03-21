package freeswitch

import (
	"fmt"
	"reflect"
	"testing"
)

func Assert(t *testing.T, cond bool, msg string) {
	if !cond {
		t.Error(msg)
		t.Fail()
	}
}
func Equals(t *testing.T, exp interface{}, act interface{}) {
	Assert(t, reflect.DeepEqual(exp, act), fmt.Sprintf("Expected %+v\nGot %+v\n", exp, act))
}

func TestHeaders_Del(t *testing.T) {
	h := headers{{"foo", "bar"}}
	Equals(t, "bar", h.get("Foo"))
	h.del("Foo")
	Equals(t, "", h.get("foo"))
}

func TestHeaders_Set(t *testing.T) {
	h := headers{{"val", "old"}}
	Equals(t, "old", h.get("VAL"))
	h.set("Val", "new")
	Equals(t, "new", h.get("VAL"))
}

func TestHeaders_Add(t *testing.T) {
	h := headers{{"foo", "bar"}}
	h.add("foo", "baz")
	Equals(t, []string{"bar", "baz"}, h.getAll("foo"))
}
