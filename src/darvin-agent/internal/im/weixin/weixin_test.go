package weixin

import (
	"encoding/json"
	"testing"
)

func TestWeixinMsgText(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"text at root", `{"item_list":[{"type":1,"text":"hello"}]}`, "hello"},
		{"text in data", `{"item_list":[{"type":2},{"type":1,"data":{"text":"hi"}}]}`, "hi"},
		{"content at root", `{"item_list":[{"type":1,"content":"你好"}]}`, "你好"},
		{"content in data", `{"item_list":[{"type":1,"data":{"content":"world"}}]}`, "world"},
		{"text under unknown type is still captured", `{"item_list":[{"type":9,"text":"keep"}]}`, "keep"},
		{"first text wins", `{"item_list":[{"type":1,"text":"a"},{"type":1,"text":"b"}]}`, "a"},
		// 真实 iLink 返回：文本在 item_list[].text_item.text
		{"text_item.text (real shape)", `{"item_list":[{"type":1,"text_item":{"text":"你好"}}]}`, "你好"},
		{"empty", `{"item_list":[]}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m weixinMsg
			if err := json.Unmarshal([]byte(tc.json), &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := m.Text(); got != tc.want {
				t.Fatalf("Text() = %q, want %q", got, tc.want)
			}
		})
	}
}
