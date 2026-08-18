package weixin

import (
	"encoding/json"
	"testing"
)

// TestLiveParseHelloFeeds the real iLink getupdates response observed via
// curl against the user's live bot (text "你好" inside text_item) through
// the same struct + Text() path the connector uses. End-to-end proof that
// the parser extracts the user-visible text.
func TestLiveParseHelloFeeds(t *testing.T) {
	live := []byte(`{"msgs":[{"seq":1,"message_id":7495426660285223688,"from_user_id":"o9cq80z1emVrULYacr8A-yN6sETQ@im.wechat","to_user_id":"686e904fa004@im.bot","client_id":"mmassistant_bypmsg_inbox_da9b30c2cf7b76c67c7c68fb0c9137a1mmo9cq800l3VfZItzwo_t32JIiLYYo@weclaw10742284_1787048971","create_time_ms":1787048974030,"update_time_ms":1787048974136,"delete_time_ms":0,"session_id":"","group_id":"","message_type":1,"message_state":2,"item_list":[{"type":1,"create_time_ms":1787048974030,"update_time_ms":1787048974030,"is_completed":true,"msg_id":"v1:14778023042603769070","button_item_list":[],"at_bot_username_list":[],"text_item":{"text":"你好"}}],"context_token":"AARzJWAFAAABAAAAAADEcaoilFNoMWIgDjSEaiAAAAB+9905Q6UiugPBawU3n3cyzQX+LkN8ofRzsCZYN0mt7mSyMBtFHbn0zRntHOY5Tp9sXXmmn5cD1HICvF1W9yVSRZowvJAI","root_id":0,"parent_id":0}],"sync_buf":"CAEQnZGxo4E0GOOQsaOBNA==","get_updates_buf":"ChAIARCdkbGjgTQY45Cxo4E0Ejo2ODZlOTA0ZmEwMDRAaW0uYm90OjA2MDAwMGEzMjU5N2IwMWI4OGM4MTA0MzRlMTliYmQxOTFkY2Nk"}`)

	var out struct {
		Ret    int         `json:"ret"`
		Msgs   []weixinMsg `json:"msgs"`
		Cursor string      `json:"get_updates_buf"`
	}
	if err := json.Unmarshal(live, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Msgs) != 1 {
		t.Fatalf("want 1 msg, got %d", len(out.Msgs))
	}
	m := out.Msgs[0]
	if m.FromUserID == "" {
		t.Fatal("from_user_id empty")
	}
	if m.ContextToken == "" {
		t.Fatal("context_token empty (would break reply)")
	}
	if got := m.Text(); got != "你好" {
		t.Fatalf("Text() = %q, want %q", got, "你好")
	}
	if out.Cursor == "" {
		t.Fatal("get_updates_buf cursor empty")
	}
	t.Logf("OK from=%s text=%q cursor_len=%d ctx_len=%d",
		m.FromUserID, m.Text(), len(out.Cursor), len(m.ContextToken))
}
