package application

import (
	"context"
	"encoding/json"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// A connection names the directories it releases, per direction and only when set, in describe and in the
// connection list; a connection without them publishes no files key. A tool with local file access is
// offered only by a connection that releases a directory of its direction.
func TestDiscoveryNamesReleasedDirectories(t *testing.T) {
	core, _ := testCore(t, []string{"plain", "reader", "both"}, nil, true)
	upload := testDescriptor("fake.files.upload", capability.EffectRead, capability.ConfirmationNone)
	upload.LocalFiles = config.LocalFilesRead
	if err := core.registry.Register("fake", capability.Operation{Descriptor: upload,
		Handler: func(_ context.Context, _ *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
			_ json.RawMessage) (any, error) {
			return nil, nil
		}}); err != nil {
		t.Fatal(err)
	}
	for name, files := range map[string]config.Files{
		"reader": {Read: []string{"/srv/in"}},
		"both":   {Read: []string{"/srv/in", "~/more"}, Write: []string{"/srv/out"}},
	} {
		connection := core.all.Connections[name]
		connection.Files = files
		core.all.Connections[name] = connection
	}

	described, err := core.Describe(DescribeRequest{Operation: "fake.files.upload"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*FilesRef{}
	for _, ref := range described.Connections {
		got[ref.Name] = ref.Files
	}
	want := map[string]*FilesRef{
		"reader": {Read: []string{"/srv/in"}},
		"both":   {Read: []string{"/srv/in", "~/more"}, Write: []string{"/srv/out"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("describe connections = %v, want only the readers %v", got, want)
	}
	if _, err := core.Describe(DescribeRequest{Operation: "fake.files.upload", Connection: "plain"}); err == nil {
		t.Error("describe through a connection without files succeeded")
	}

	summaries := map[string]*FilesRef{}
	for _, summary := range core.Connections("", nil).Connections {
		summaries[summary.Name] = summary.Files
	}
	wantSummaries := map[string]*FilesRef{"plain": nil, "reader": want["reader"], "both": want["both"]}
	if !reflect.DeepEqual(summaries, wantSummaries) {
		t.Errorf("connections = %v, want %v", summaries, wantSummaries)
	}
	var plain map[string]any
	for _, summary := range core.Connections("", nil).Connections {
		if summary.Name == "plain" {
			data, _ := json.Marshal(summary)
			_ = json.Unmarshal(data, &plain)
		}
	}
	if _, ok := plain["files"]; ok {
		t.Errorf("a connection without files publishes a files key: %v", plain)
	}
}
