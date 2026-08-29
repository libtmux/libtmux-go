package mcp_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/libtmux/libtmux-go/tmux"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

//libtmux:real-tmux
func TestResourcesAddressTheHierarchy(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: addressed\nwindows:\n  - window_name: only\n" +
			"    panes:\n      - shell: sh -c 'printf RESOURCE-MARK; sleep 300'\n",
	}, nil)

	listed, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(listed.Resources) == 0 {
		t.Fatal("the server advertises no resources")
	}

	templates, err := session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	if len(templates.ResourceTemplates) < 4 {
		t.Fatalf("advertised %d templates, want the hierarchy",
			len(templates.ResourceTemplates))
	}

	// The whole hierarchy, by name.
	read, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: "tmux://sessions"})
	if err != nil {
		t.Fatalf("read sessions: %v", err)
	}
	if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, "addressed") {
		t.Fatalf("tmux://sessions does not describe the session: %+v", read.Contents)
	}

	// One pane's contents, as text a person would paste.
	panes := paneIDs(ctx, t, session)
	if len(panes) == 0 {
		t.Fatal("no panes")
	}

	// Which spellings a URI takes, pinned rather than assumed. Every tool hands
	// a pane back as %1, so a client composing a URI from one is the likely
	// path, and a read and a subscription of the same string must not disagree
	// about whether it is a URI at all.
	bare := strings.TrimPrefix(panes[0], "%")
	for _, spelling := range []struct {
		uri      string
		readable bool
		why      string
	}{
		{"tmux://panes/" + bare + "/content", true, "the form the templates and completions give"},
		{"tmux://panes/%25" + bare + "/content", true, "the sigil, percent-encoded"},
		{
			"tmux://panes/%" + bare + "/content", false,
			"the sigil raw, which no URI can carry: % begins an escape",
		},
	} {
		_, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: spelling.uri})
		if (err == nil) != spelling.readable {
			t.Errorf("read %s (%s): error = %v, want readable = %t",
				spelling.uri, spelling.why, err, spelling.readable)
		}
		// Subscription is routed by the string itself rather than by template,
		// so it takes the raw sigil too and must keep doing so: a client that
		// subscribed and got silence is the defect that bought this.
		if err := session.Subscribe(ctx, &sdk.SubscribeParams{URI: spelling.uri}); err != nil {
			t.Errorf("subscribe %s (%s): %v", spelling.uri, spelling.why, err)
		}
		if err := session.Unsubscribe(ctx, &sdk.UnsubscribeParams{URI: spelling.uri}); err != nil {
			t.Errorf("unsubscribe %s: %v", spelling.uri, err)
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		read, err = session.ReadResource(ctx, &sdk.ReadResourceParams{
			URI: "tmux://panes/" + strings.TrimPrefix(panes[0], "%") + "/content",
		})
		if err != nil {
			t.Fatalf("read pane content: %v", err)
		}
		if len(read.Contents) != 0 && strings.Contains(read.Contents[0].Text, "RESOURCE-MARK") {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, "RESOURCE-MARK") {
		t.Errorf("pane content resource does not show the pane: %+v", read.Contents)
	}
	if got := read.Contents[0].MIMEType; got != "text/plain" {
		t.Errorf("pane content is %q, want text/plain", got)
	}

	// A pane's identity, as JSON.
	read, err = session.ReadResource(ctx, &sdk.ReadResourceParams{URI: "tmux://panes/" + strings.TrimPrefix(panes[0], "%")})
	if err != nil {
		t.Fatalf("read pane: %v", err)
	}
	if !strings.Contains(read.Contents[0].Text, panes[0]) {
		t.Errorf("pane resource does not name the pane: %s", read.Contents[0].Text)
	}

	// A URI the server does not serve is refused rather than guessed at.
	if _, err := session.ReadResource(ctx, &sdk.ReadResourceParams{
		URI: "tmux://nonsense/1",
	}); err == nil {
		t.Error("an unknown resource URI was accepted")
	}
}

//libtmux:real-tmux
func TestResourceURIsArePercentDecoded(t *testing.T) {
	session, _, ctx := connect(t)
	call(ctx, t, session, "build_workspace", map[string]any{
		"document": "session_name: spaced name\nwindows:\n  - window_name: only\n" +
			"    panes:\n      - shell: sleep 300\n",
	}, nil)

	// A session whose name needs an escape has exactly one legal spelling.
	read, err := session.ReadResource(ctx, &sdk.ReadResourceParams{
		URI: "tmux://sessions/spaced%20name/windows",
	})
	if err != nil {
		t.Fatalf("read windows of an escaped session name: %v", err)
	}
	if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, "only") {
		t.Errorf("an escaped session name selects no windows: %+v", read.Contents)
	}

	// %25 is how a client encodes the sigil tmux prints, so %250 is pane %0.
	panes := paneIDs(ctx, t, session)
	if len(panes) == 0 {
		t.Fatal("no panes")
	}
	encoded := "tmux://panes/%25" + strings.TrimPrefix(panes[0], "%")
	read, err = session.ReadResource(ctx, &sdk.ReadResourceParams{URI: encoded})
	if err != nil {
		t.Fatalf("read %s: %v", encoded, err)
	}
	if len(read.Contents) == 0 || !strings.Contains(read.Contents[0].Text, panes[0]) {
		t.Errorf("%s does not describe pane %s: %+v", encoded, panes[0], read.Contents)
	}
}

//libtmux:real-tmux
func TestResourcesSurviveAReadOnlyServer(t *testing.T) {
	t.Setenv("LIBTMUX_SAFETY", "readonly")
	session, _, ctx := connect(t)
	listed, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	if len(listed.Resources) == 0 {
		t.Error("a read-only server offers no resources to browse")
	}
}

//libtmux:real-tmux
func TestAResourceNamingNothingSaysSoInTheProtocol(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: absent\nwindows:\n  - panes:\n      - {}\n")

	for uri, want := range map[string]string{
		"tmux://panes/9000":         "no pane %9000",
		"tmux://panes/9000/content": "no pane %9000",
		"tmux://windows/9000":       "no window @9000",
		"tmux://windows/9000/panes": "no window @9000",
		"tmux://sessions/nowhere":   `no session named "nowhere"`,
	} {
		_, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri})
		if err == nil {
			t.Errorf("%s was read", uri)
			continue
		}
		var wire *jsonrpc.Error
		if !errors.As(err, &wire) {
			t.Errorf("%s failed as %T, not a JSON-RPC error", uri, err)
			continue
		}
		if wire.Code != sdk.CodeResourceNotFound {
			t.Errorf("%s failed with code %d, want %d",
				uri, wire.Code, sdk.CodeResourceNotFound)
		}
		if !strings.Contains(wire.Message, want) {
			t.Errorf("%s says %q, want it to name %q", uri, wire.Message, want)
		}
	}
}

//libtmux:real-tmux
func TestEveryAdvertisedResourceCanBeRead(t *testing.T) {
	session, _, ctx := connect(t)
	workspace(ctx, t, session, "session_name: readable\nwindows:\n"+
		"  - panes:\n      - {}\n")

	var listed struct {
		Panes []struct {
			ID       string `json:"id"`
			WindowID string `json:"windowId"`
			Session  string `json:"session"`
		} `json:"panes"`
	}
	call(ctx, t, session, "list_panes", map[string]any{}, &listed)
	if len(listed.Panes) == 0 {
		t.Fatal("no panes to address")
	}
	only := listed.Panes[0]
	// Without the sigil, which is the form the descriptions now name and the
	// only one a template can match.
	bare := func(id string) string { return strings.TrimLeft(id, "%@$") }

	templates, err := session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	filled := map[string]string{
		"tmux://sessions/{session}":         "tmux://sessions/" + only.Session,
		"tmux://sessions/{session}/windows": "tmux://sessions/" + only.Session + "/windows",
		"tmux://windows/{window}":           "tmux://windows/" + bare(only.WindowID),
		"tmux://windows/{window}/panes":     "tmux://windows/" + bare(only.WindowID) + "/panes",
		"tmux://panes/{pane}":               "tmux://panes/" + bare(only.ID),
		"tmux://panes/{pane}/content":       "tmux://panes/" + bare(only.ID) + "/content",
	}
	// A template whose blank had to lose a sigil to be readable has to say
	// that it did. Every tool hands an id back with one, so the description is
	// the only place a client learns to take it off, and a template silent
	// about it reads as one that takes what the tools returned.
	sigilled := map[string]bool{
		"tmux://windows/{window}":       true,
		"tmux://windows/{window}/panes": true,
		"tmux://panes/{pane}":           true,
		"tmux://panes/{pane}/content":   true,
	}
	if len(templates.ResourceTemplates) != len(filled) {
		t.Errorf("%d templates advertised, %d covered here",
			len(templates.ResourceTemplates), len(filled))
	}
	for _, template := range templates.ResourceTemplates {
		uri, ok := filled[template.URITemplate]
		if !ok {
			t.Errorf("%s is advertised and this test does not read it", template.URITemplate)
			continue
		}
		if sigilled[template.URITemplate] &&
			!strings.Contains(template.Description, "without its sigil") {
			t.Errorf("%s takes an id with a sigil stripped and says %q",
				template.URITemplate, template.Description)
		}
		if _, err := session.ReadResource(ctx, &sdk.ReadResourceParams{URI: uri}); err != nil {
			t.Errorf("%s advertises %q, and reading %s says: %v",
				template.URITemplate, template.Description, uri, err)
		}
	}
}

//libtmux:real-tmux
func TestTheEnvironmentListingWithholdsValues(t *testing.T) {
	session, target, ctx := connect(t)
	workspace(ctx, t, session, "session_name: secrets\nwindows:\n  - panes:\n      - {}\n")

	const secret = "s3cr3t-value-nobody-asked-for"
	for name, value := range map[string]string{
		"PROBE_LOOKS_LIKE_A_TOKEN": secret,
		"PROBE_ORDINARY":           "plain",
	} {
		if err := target.SetEnvironment(ctx, name, value,
			tmux.SetEnvironmentOptions{}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}

	var listed struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"variables"`
		ValuesWithheld bool `json:"valuesWithheld"`
	}
	result := call(ctx, t, session, "show_environment", map[string]any{}, &listed)
	if result.IsError {
		t.Fatalf("show_environment: %#v", result.Content)
	}
	if len(listed.Variables) == 0 {
		t.Fatal("the listing is empty, so it proves nothing")
	}
	if !listed.ValuesWithheld {
		t.Error("the listing does not say it withheld the values")
	}
	seeded := false
	for _, entry := range listed.Variables {
		if entry.Value != "" {
			t.Errorf("the listing carries a value for %s", entry.Name)
		}
		if entry.Name == "PROBE_LOOKS_LIKE_A_TOKEN" {
			seeded = true
		}
	}
	if !seeded {
		t.Error("the listing omits a variable that is set, so names are not enough")
	}
	// Nothing in the whole reply, not only the field this test reads.
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok &&
			strings.Contains(text.Text, secret) {
			t.Error("the reply text carries the value")
		}
	}

	// Naming one returns its value, which is the narrower ask.
	var one struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"variables"`
		ValuesWithheld bool `json:"valuesWithheld"`
	}
	call(ctx, t, session, "show_environment", map[string]any{
		"name": "PROBE_LOOKS_LIKE_A_TOKEN",
	}, &one)
	if len(one.Variables) != 1 || one.Variables[0].Value != secret {
		t.Errorf("naming a variable did not return its value: %+v", one.Variables)
	}
	if one.ValuesWithheld {
		t.Error("a named read reports its value withheld")
	}

	// And the listing is bounded like its peers.
	var few struct {
		Variables []struct{} `json:"variables"`
		Truncated bool       `json:"truncated"`
	}
	call(ctx, t, session, "show_environment", map[string]any{"maxLines": 2}, &few)
	if len(few.Variables) > 2 {
		t.Errorf("maxLines 2 returned %d variables", len(few.Variables))
	}
	if len(listed.Variables) > 2 && !few.Truncated {
		t.Error("a bounded listing does not report the truncation")
	}
}
