package cloud

import (
	"reflect"
	"testing"
)

func TestOrderedParams(t *testing.T) {
	if got := OrderedParams(nil); got != nil {
		t.Fatalf("nil params = %v", got)
	}
	// 全部数字键按数值升序。
	num := OrderedParams(map[string]string{"10": "j", "2": "b", "1": "a"})
	if !reflect.DeepEqual(num, []string{"a", "b", "j"}) {
		t.Fatalf("numeric order = %v", num)
	}
	// 含非数字键时按键名字典序。
	mixed := OrderedParams(map[string]string{"b": "2", "a": "1", "c": "3"})
	if !reflect.DeepEqual(mixed, []string{"1", "2", "3"}) {
		t.Fatalf("lexical order = %v", mixed)
	}
}

func TestSmsTemplateParams(t *testing.T) {
	if got := SmsTemplateParams(nil); got != nil {
		t.Fatalf("nil request = %v", got)
	}
	// TemplateParams 非空时优先。
	req := &SmsRequest{
		TemplateParams: []string{"x", "y"},
		Params:         map[string]string{"1": "z"},
	}
	if got := SmsTemplateParams(req); !reflect.DeepEqual(got, []string{"x", "y"}) {
		t.Fatalf("explicit params = %v", got)
	}
	// 否则展开 Params。
	req = &SmsRequest{Params: map[string]string{"0": "a", "1": "b"}}
	if got := SmsTemplateParams(req); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("expanded params = %v", got)
	}
}
