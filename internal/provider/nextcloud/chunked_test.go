package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	uploadsPrefix = "/remote.php/dav/uploads/" + aliceUser + "/"
	fileETag      = "3a71bd0c1"
)

// seen is one recorded request with the headers the chunked upload depends on.
type seen struct {
	method, path, destination, total, ifMatch, ifNoneMatch, body string
}

// chunkServer is a fake Nextcloud for one chunked upload. A step fails when it has an entry in failures.
type chunkServer struct {
	exists   bool
	failures map[string]int // "MKCOL", "PUT2", "MOVE" -> status; 0 in the map with value -1 means transport error
	log      []seen
	calls    *[]call
}

func (s *chunkServer) handler(t *testing.T) func(*http.Request) (*http.Response, error) {
	return func(request *http.Request) (*http.Response, error) {
		// serve has recorded the request and drained its body already.
		body := (*s.calls)[len(*s.calls)-1].body
		s.log = append(s.log, seen{request.Method, request.URL.Path, request.Header.Get("Destination"), request.Header.Get("OC-Total-Length"),
			request.Header.Get("If-Match"), request.Header.Get("If-None-Match"), body})
		key := request.Method
		if request.Method == http.MethodPut {
			key += request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]
		}
		if status, ok := s.failures[key]; ok {
			if status < 0 {
				return nil, errors.New("connection reset by peer")
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(bodyCanary))}, nil
		}
		switch request.Method {
		case methodPropfind:
			if !s.exists {
				return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/big.bin", "big.bin", "1004", "5"))), nil
		case "MKCOL", http.MethodPut, http.MethodDelete:
			return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		case "MOVE":
			return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{"Etag": []string{`"moved"`}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		t.Errorf("unexpected method %s", request.Method)
		return nil, errors.New("unexpected")
	}
}

func (s *chunkServer) methods() string {
	var out []string
	for _, r := range s.log {
		out = append(out, r.method)
	}
	return strings.Join(out, " ")
}

// chunkedUpload writes "0123456789" with a chunk size of 4 and a single-request limit of 5.
func chunkedUpload(t *testing.T, server *chunkServer, create bool, path string) (any, error) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "in.bin")
	if err := os.WriteFile(source, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	prevChunk, prevMax := chunkSize, maxPathUploadBytes
	chunkSize, maxPathUploadBytes = 4, 5
	t.Cleanup(func() { chunkSize, maxPathUploadBytes = prevChunk, prevMax })
	server.calls = serve(t, server.handler(t))
	red := &redact.Redactor{}
	args := `{"path":` + strconv.Quote(path) + `,"local_path":` + strconv.Quote(source)
	if create {
		return invokeFilesCreate(context.Background(), localConnection(dir, ""), resolver(red), red, json.RawMessage(args+`}`))
	}
	return invokeFilesUpdate(context.Background(), localConnection(dir, ""), resolver(red), red, json.RawMessage(args+`,"etag":"`+fileETag+`"}`))
}

func TestChunkedCreateFollowsTheProtocol(t *testing.T) {
	server := &chunkServer{}
	result, err := chunkedUpload(t, server, true, "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	got := result.(map[string]any)
	if got["created"] != true || got["method"] != "chunked" || got["size"] != int64(10) ||
		got["sha256"] != "84d89877f0d4041efb6bf91a16f0248f2fd573e6af05c19f96bedb9f882f7882" || got["etag"] != "moved" {
		t.Errorf("result = %+v", got)
	}
	if want := "PROPFIND MKCOL PUT PUT PUT PROPFIND MOVE"; server.methods() != want {
		t.Fatalf("methods = %s, want %s", server.methods(), want)
	}
	id := regexp.MustCompile(`^` + uploadsPrefix + `([0-9a-f]{32})$`).FindStringSubmatch(server.log[1].path)
	if id == nil {
		t.Fatalf("upload folder = %s", server.log[1].path)
	}
	destination := "/remote.php/dav/files/alice/Reports/big.bin"
	for i, want := range map[int]string{2: "0123", 3: "4567", 4: "89"} {
		r := server.log[i]
		if r.path != uploadsPrefix+id[1]+"/"+strconv.Itoa(i-1) || r.body != want || r.destination != destination || r.total != "10" {
			t.Errorf("chunk request %d = %+v", i, r)
		}
	}
	move := server.log[6]
	if move.path != uploadsPrefix+id[1]+"/.file" || move.destination != destination || move.total != "10" {
		t.Errorf("move = %+v", move)
	}
	if server.log[1].destination != destination {
		t.Errorf("mkcol = %+v", server.log[1])
	}
}

func TestChunkedUploadIDsDiffer(t *testing.T) {
	a, _ := uploadID()
	b, _ := uploadID()
	if a == b || len(a) != 32 {
		t.Errorf("ids = %s, %s", a, b)
	}
}

func TestChunkedUpdateChecksTheVersionBeforeTheMove(t *testing.T) {
	server := &chunkServer{exists: true}
	result, err := chunkedUpload(t, server, false, "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(map[string]any); got["updated"] != true || got["method"] != "chunked" {
		t.Errorf("result = %+v", got)
	}
	if want := "PROPFIND MKCOL PUT PUT PUT PROPFIND MOVE"; server.methods() != want {
		t.Errorf("methods = %s", server.methods())
	}
}

func TestChunkedCreateConflictAndStaleUpdateSendNothing(t *testing.T) {
	server := &chunkServer{exists: true}
	if _, err := chunkedUpload(t, server, true, "big.bin"); err == nil || server.methods() != "PROPFIND" {
		t.Errorf("create over existing: err = %v, methods = %s", err, server.methods())
	}
	server = &chunkServer{} // the file to update is gone
	if _, err := chunkedUpload(t, server, false, "big.bin"); err == nil || server.methods() != "PROPFIND" {
		t.Errorf("update of missing: err = %v, methods = %s", err, server.methods())
	}
}

func TestChunkedTargetTakenDuringTheUploadIsNotMoved(t *testing.T) {
	server := &chunkServer{}
	inner := server.handler(t)
	puts := 0
	dir := t.TempDir()
	source := filepath.Join(dir, "in.bin")
	_ = os.WriteFile(source, []byte("0123456789"), 0o600)
	prevChunk, prevMax := chunkSize, maxPathUploadBytes
	chunkSize, maxPathUploadBytes = 4, 5
	t.Cleanup(func() { chunkSize, maxPathUploadBytes = prevChunk, prevMax })
	server.calls = serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			if puts++; puts == 3 {
				server.exists = true
			}
		}
		return inner(r)
	})
	red := &redact.Redactor{}
	_, err := invokeFilesCreate(context.Background(), localConnection(dir, ""), resolver(red), red,
		json.RawMessage(`{"path":"big.bin","local_path":`+strconv.Quote(source)+`}`))
	if err == nil || strings.Contains(server.methods(), "MOVE") || !strings.HasSuffix(server.methods(), "PROPFIND DELETE") {
		t.Errorf("err = %v, methods = %s", err, server.methods())
	}
}

func TestChunkFailureStopsCleansUpAndNeverRepeats(t *testing.T) {
	for name, status := range map[string]int{"clear": http.StatusInsufficientStorage, "server": http.StatusBadGateway, "transport": -1} {
		t.Run(name, func(t *testing.T) {
			server := &chunkServer{failures: map[string]int{"PUT2": status}}
			_, err := chunkedUpload(t, server, true, "big.bin")
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "may have been stored") {
				t.Fatalf("err = %v", err)
			}
			if want := "PROPFIND MKCOL PUT PUT DELETE"; server.methods() != want {
				t.Errorf("methods = %s, want %s", server.methods(), want)
			}
			last := server.log[len(server.log)-1]
			if !strings.HasPrefix(last.path, uploadsPrefix) || strings.Contains(last.path, "/files/") || last.destination != "" {
				t.Errorf("cleanup = %+v", last)
			}
		})
	}
}

func TestMkcolFailureCleansUp(t *testing.T) {
	server := &chunkServer{failures: map[string]int{"MKCOL": http.StatusForbidden}}
	_, err := chunkedUpload(t, server, true, "big.bin")
	if classOf(err) != provider.ClassPermission || server.methods() != "PROPFIND MKCOL DELETE" {
		t.Errorf("err = %v, methods = %s", err, server.methods())
	}
}

func TestChunkedMoveOutcomes(t *testing.T) {
	for name, tt := range map[string]struct {
		status  int
		unsure  bool
		cleanup bool
	}{
		"conflict": {http.StatusConflict, false, true}, "precondition": {http.StatusPreconditionFailed, false, true},
		"server error": {http.StatusInternalServerError, true, false}, "timeout gateway": {http.StatusGatewayTimeout, true, false},
		"transport": {-1, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			server := &chunkServer{failures: map[string]int{"MOVE": tt.status}}
			_, err := chunkedUpload(t, server, true, "big.bin")
			if err == nil || strings.Contains(err.Error(), bodyCanary) ||
				strings.Contains(err.Error(), "stat the file before repeating") != tt.unsure {
				t.Fatalf("err = %v", err)
			}
			want := "PROPFIND MKCOL PUT PUT PUT PROPFIND MOVE"
			if tt.cleanup {
				want += " DELETE"
			}
			if server.methods() != want {
				t.Errorf("methods = %s, want %s", server.methods(), want)
			}
		})
	}
}

func TestSinglePutUncertainOutcomes(t *testing.T) {
	for name, tt := range map[string]struct {
		status int
		unsure bool
	}{"500": {500, true}, "503": {503, true}, "transport": {-1, true}, "conflict": {409, false}, "precondition": {412, false}, "forbidden": {403, false}} {
		t.Run(name, func(t *testing.T) {
			calls := serve(t, func(*http.Request) (*http.Response, error) {
				if tt.status < 0 {
					return nil, errors.New("connection reset by peer")
				}
				return &http.Response{StatusCode: tt.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(bodyCanary))}, nil
			})
			red := &redact.Redactor{}
			_, err := invokeFilesCreate(context.Background(), localConnection("", ""), resolver(red), red,
				json.RawMessage(`{"path":"a.txt","content_base64":"aGk="}`))
			if err == nil || strings.Contains(err.Error(), bodyCanary) || len(*calls) != 1 ||
				strings.Contains(err.Error(), "stat the file before repeating") != tt.unsure {
				t.Errorf("err = %v, calls = %d", err, len(*calls))
			}
			if tt.unsure && classOf(err) == "" {
				t.Errorf("class lost: %v", err)
			}
		})
	}
}

func TestChunkedUploadOutsideTheRootAndOverTheChunkLimitSendNothing(t *testing.T) {
	refuse(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "in.bin")
	_ = os.WriteFile(source, make([]byte, 20), 0o600)
	prevChunk, prevMax := chunkSize, maxPathUploadBytes
	chunkSize, maxPathUploadBytes = 1, 5
	t.Cleanup(func() { chunkSize, maxPathUploadBytes = prevChunk, prevMax })
	red := &redact.Redactor{}
	for _, path := range []string{"../x.bin", "/abs.bin", "a/../b.bin"} {
		_, err := invokeFilesCreate(context.Background(), localConnection(dir, ""), resolver(red), red,
			json.RawMessage(`{"path":`+strconv.Quote(path)+`,"local_path":`+strconv.Quote(source)+`}`))
		if err == nil {
			t.Errorf("%s accepted", path)
		}
	}
	maxPathUploadBytes = 4
	if err := os.WriteFile(source, make([]byte, maxChunks+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeFilesCreate(context.Background(), localConnection(dir, ""), resolver(red), red,
		json.RawMessage(`{"path":"a.bin","local_path":`+strconv.Quote(source)+`}`)); err == nil {
		t.Error("a file needing more than the maximum number of chunks was accepted")
	}
}
