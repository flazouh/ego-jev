package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as a fake ego-browser. With EGO_JEV_FAKE set it reads the bridge config out of the
// `nodejs -e <script>` arguments, connects to the socket, and answers according to the mode.
func TestMain(m *testing.M) {
	if mode := os.Getenv("EGO_JEV_FAKE"); mode != "" {
		os.Exit(fakeEgo(mode, os.Args))
	}
	os.Exit(m.Run())
}

func fakeEgo(mode string, args []string) int {
	switch mode {
	case "exit":
		fmt.Fprintln(os.Stderr, "task space not found: 99")
		return 13
	case "hang":
		time.Sleep(time.Minute)
		return 0
	}
	script := args[len(args)-1]
	start := strings.Index(script, "const CONFIG = ") + len("const CONFIG = ")
	end := strings.Index(script[start:], ";\n")
	var config Config
	if err := json.Unmarshal([]byte(script[start:start+end]), &config); err != nil {
		fmt.Fprintln(os.Stderr, "bad config:", err)
		return 2
	}
	conn, err := net.Dial("unix", config.Socket)
	if err != nil {
		return 3
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return 0
		}
		var req message
		json.Unmarshal(line, &req)
		reply := message{ID: req.ID}
		switch {
		case req.Method == "hello":
			reply.Result = json.RawMessage(fmt.Sprintf(`{"spaceId":%d,"page":%q,"url":"about:blank"}`, 7, config.Page))
		case mode == "bad-id" && req.Method == "observe":
			reply.ID = req.ID + 100
		case mode == "extra-field" && req.Method == "observe":
			reply.Result = json.RawMessage(`{"url":"u","title":"t","text":"","elements":[],"viewport":{"width":1,"height":1},"scroll":{"up":false,"down":false},"surprise":1}`)
		case req.Method == "observe":
			reply.Result = json.RawMessage(`{"url":"https://x.test/","title":"X","heading":"Welcome","text":"hi","elements":[{"id":"e1","role":"button","label":"Go","context":"Dialog"}],"viewport":{"width":800,"height":600},"scroll":{"up":false,"down":true}}`)
		case req.Method == "finish":
			reply.Result = json.RawMessage(`{}`)
		default:
			reply.Error = "unknown method " + req.Method
		}
		out, _ := json.Marshal(reply)
		conn.Write(append(out, '\n'))
		if req.Method == "finish" {
			return 0
		}
	}
}

func start(t *testing.T, mode string, opts Options) (*Bridge, error) {
	t.Helper()
	t.Setenv("EGO_JEV_FAKE", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	opts.Binary = exe
	return Start(context.Background(), opts)
}

func TestStartResumesASpaceAndObserves(t *testing.T) {
	b, err := start(t, "ok", Options{Space: 7, Page: "p2"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Info.SpaceID != 7 || b.Info.Page != "p2" {
		t.Fatalf("info = %+v", b.Info)
	}
	o, err := b.Observe(200, 3000)
	if err != nil || o.Title != "X" || o.Heading != "Welcome" || len(o.Elements) != 1 || o.Elements[0].Context != "Dialog" || !o.Scroll.Down {
		t.Fatalf("observe = %+v, %v", o, err)
	}
	if err := b.Close(true); err != nil {
		t.Fatal(err)
	}
}

func TestStartReportsAnEarlyExitWithItsOutput(t *testing.T) {
	_, err := start(t, "exit", Options{})
	if err == nil || !strings.Contains(err.Error(), "task space not found: 99") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartTimesOutWhenTheBridgeNeverConnects(t *testing.T) {
	began := time.Now()
	_, err := start(t, "hang", Options{StartTimeout: 300 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "did not connect") || time.Since(began) > 10*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(began))
	}
}

func TestAMismatchedReplyBreaksTheBridge(t *testing.T) {
	b, err := start(t, "bad-id", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Observe(1, 1); err == nil || !strings.Contains(err.Error(), "does not match request id") {
		t.Fatalf("first err = %v", err)
	}
	if _, err := b.Observe(1, 1); err == nil || !strings.Contains(err.Error(), "unusable") {
		t.Fatalf("second err = %v", err)
	}
	if err := b.Close(true); err == nil || !strings.Contains(err.Error(), "not finished") {
		t.Fatalf("close err = %v", err)
	}
}

func TestAnUnknownFieldInAReplyIsAContractError(t *testing.T) {
	b, err := start(t, "extra-field", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(false)
	if _, err := b.Observe(1, 1); err == nil || !strings.Contains(err.Error(), "does not match the Go types") {
		t.Fatalf("err = %v", err)
	}
}

func TestTheEmbeddedScriptHasTheConfigMarker(t *testing.T) {
	if strings.Count(bridgeScript, ConfigMarker) != 1 || !strings.Contains(bridgeScript, "const CONFIG = "+ConfigMarker+";\n") {
		t.Fatal("bridge.js must contain exactly one `const CONFIG = " + ConfigMarker + ";` line")
	}
}
