package cdp

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeBrowser answers CDP commands the way Chrome does on
// --remote-debugging-pipe: NUL-terminated JSON on both directions.
func fakeBrowser(t *testing.T, in *os.File, out *os.File, handle func(req map[string]interface{}) interface{}) {
	t.Helper()
	go func() {
		defer out.Close()
		br := bufio.NewReader(in)
		for {
			msg, err := br.ReadBytes(0)
			if err != nil {
				return
			}
			var req map[string]interface{}
			if err := json.Unmarshal(msg[:len(msg)-1], &req); err != nil {
				t.Errorf("bad frame %q: %v", msg, err)
				return
			}
			// An unrelated event first: the client must skip it.
			ev, _ := json.Marshal(map[string]interface{}{"method": "Target.targetCreated", "params": map[string]interface{}{}})
			_, _ = out.Write(append(ev, 0))
			resp := map[string]interface{}{"id": req["id"], "result": handle(req)}
			if sid, ok := req["sessionId"]; ok {
				resp["sessionId"] = sid
			}
			b, _ := json.Marshal(resp)
			if _, err := out.Write(append(b, 0)); err != nil {
				return
			}
		}
	}()
}

func TestPipeAttachAndSessionCalls(t *testing.T) {
	cmdR, cmdW, _ := os.Pipe()
	respR, respW, _ := os.Pipe()
	big := strings.Repeat("x", 200<<10) // larger than the reader's buffer
	fakeBrowser(t, cmdR, respW, func(req map[string]interface{}) interface{} {
		switch req["method"] {
		case "Target.getTargets":
			return map[string]interface{}{"targetInfos": []map[string]interface{}{
				{"targetId": "W1", "type": "service_worker"},
				{"targetId": "T1", "type": "page"},
			}}
		case "Target.attachToTarget":
			params, _ := req["params"].(map[string]interface{})
			if params["targetId"] != "T1" || params["flatten"] != true {
				t.Errorf("attach params %v", params)
			}
			return map[string]interface{}{"sessionId": "S1"}
		case "Runtime.evaluate":
			if req["sessionId"] != "S1" {
				t.Errorf("page command without session: %v", req)
			}
			return map[string]interface{}{"value": big}
		}
		return map[string]interface{}{}
	})
	c := ConnectPipe(respR, cmdW)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	page, err := AttachFirstPage(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if page.ID() != "S1" {
		t.Fatalf("session %q", page.ID())
	}
	raw, err := page.Call(ctx, "Runtime.evaluate", map[string]interface{}{"expression": "1"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Value string }
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Value) != len(big) {
		t.Fatalf("large reply mangled: %d bytes, err %v", len(out.Value), err)
	}
}

func TestPipeCreatesPageWhenNoneExists(t *testing.T) {
	cmdR, cmdW, _ := os.Pipe()
	respR, respW, _ := os.Pipe()
	fakeBrowser(t, cmdR, respW, func(req map[string]interface{}) interface{} {
		switch req["method"] {
		case "Target.getTargets":
			return map[string]interface{}{"targetInfos": []interface{}{}}
		case "Target.createTarget":
			return map[string]interface{}{"targetId": "NEW"}
		case "Target.attachToTarget":
			return map[string]interface{}{"sessionId": "S2"}
		}
		return map[string]interface{}{}
	})
	c := ConnectPipe(respR, cmdW)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	page, err := AttachFirstPage(ctx, c)
	if err != nil || page.ID() != "S2" {
		t.Fatalf("page %v err %v", page, err)
	}
}

func TestPipeBrowserExitUnblocksCalls(t *testing.T) {
	cmdR, cmdW, _ := os.Pipe()
	respR, respW, _ := os.Pipe()
	_ = cmdR.Close()
	_ = respW.Close() // browser gone: reader sees EOF
	c := ConnectPipe(respR, cmdW)
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, "Target.getTargets", nil); err == nil {
		t.Fatal("call on a dead pipe must fail")
	}
	if ctx.Err() != nil {
		t.Fatal("call hung until the deadline instead of failing fast")
	}
}
