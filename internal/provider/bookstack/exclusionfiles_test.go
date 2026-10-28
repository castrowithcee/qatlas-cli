package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The page each object of the file server belongs to; 9999 is a page the instance does not know, 0 none.
var (
	commentPages    = map[int]int{71: 111, 72: 222, 73: 333, 74: 444, 75: 555, 76: 9999}
	attachmentPages = map[int]int{81: 111, 82: 555, 83: 222}
	imagePages      = map[int]int{91: 111, 92: 222, 93: 333, 94: 0, 95: 555, 96: 9999}
)

// fileServer adds comments, attachments, images, and imports to the area server. The list routes ignore every
// filter and answer all rows, so only the client check holds the boundary.
func newFileServer(t *testing.T) *areaServer {
	t.Helper()
	s := newAreaServer(t)
	inner := s.Config.Handler
	rows := func(pages map[int]int, key string, extra map[string]any) []map[string]any {
		var out []map[string]any
		for id := range pages {
			row := map[string]any{"id": id, key: pages[id]}
			for k, v := range extra {
				row[k] = v
			}
			out = append(out, row)
		}
		slices.SortFunc(out, func(a, b map[string]any) int { return a["id"].(int) - b["id"].(int) })
		return out
	}
	one := func(w http.ResponseWriter, r *http.Request, prefix string, pages map[int]int, key string, extra map[string]any) {
		id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, prefix))
		page, ok := pages[id]
		switch {
		case !ok:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			row := map[string]any{"id": id, key: page}
			for k, v := range extra {
				row[k] = v
			}
			_ = json.NewEncoder(w).Encode(row)
		}
	}
	image := map[string]any{"type": "gallery", "name": "n", "url": "https://wiki.example/uploads/a.png"}
	listRoute := func(w http.ResponseWriter, r *http.Request, all []map[string]any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": all, "total": len(all)})
	}
	s.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/api/comments" && r.Method == http.MethodGet:
			s.rec.record(r)
			listRoute(w, r, rows(commentPages, "commentable_id", map[string]any{"commentable_type": "page", "local_id": 1}))
		case path == "/api/comments" && r.Method == http.MethodPost:
			s.rec.record(r)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 99, "commentable_id": 111, "commentable_type": "page"})
		case strings.HasPrefix(path, "/api/comments/") && commentID(path) >= 71:
			s.rec.record(r)
			one(w, r, "/api/comments/", commentPages, "commentable_id", map[string]any{"commentable_type": "page", "local_id": 1})
		case path == "/api/attachments" && r.Method == http.MethodGet:
			s.rec.record(r)
			listRoute(w, r, rows(attachmentPages, "uploaded_to", nil))
		case path == "/api/attachments" && r.Method == http.MethodPost:
			s.rec.record(r)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 99, "uploaded_to": 111})
		case strings.HasPrefix(path, "/api/attachments/"):
			s.rec.record(r)
			one(w, r, "/api/attachments/", attachmentPages, "uploaded_to", map[string]any{"external": true, "name": "n"})
		case path == "/api/image-gallery" && r.Method == http.MethodGet:
			s.rec.record(r)
			listRoute(w, r, rows(imagePages, "uploaded_to", image))
		case path == "/api/image-gallery" && r.Method == http.MethodPost:
			s.rec.record(r)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 99, "uploaded_to": 111, "type": "gallery"})
		case strings.HasPrefix(path, "/api/image-gallery/") && strings.HasSuffix(path, "/data"):
			s.rec.record(r)
			_, _ = w.Write([]byte("png"))
		case strings.HasPrefix(path, "/api/image-gallery/"):
			s.rec.record(r)
			one(w, r, "/api/image-gallery/", imagePages, "uploaded_to", image)
		case strings.HasPrefix(path, "/api/imports/"):
			s.rec.record(r)
			if r.Method == http.MethodPost {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": 99})
				return
			}
			kind := map[string]string{"5": "chapter", "6": "page", "7": "book"}[strings.TrimPrefix(path, "/api/imports/")]
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 5, "type": kind})
		default:
			inner.ServeHTTP(w, r)
		}
	})
	return s
}

func commentID(path string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/comments/"))
	return n
}

// fileCall is callExcluding with a directory released for reading and writing.
func (s *areaServer) fileCall(t *testing.T, tool, args, dir string, targets, excludes []string) (any, error) {
	t.Helper()
	t.Setenv("TEST_TOKEN_ID", canaryID)
	t.Setenv("TEST_TOKEN_SECRET", canarySecret)
	resolved := boundResolved(s.URL, targets...)
	resolved.Exclude = excludes
	resolved.Files = config.Files{Read: []string{dir}, Write: []string{dir}}
	return lookup(t, tool)(context.Background(), resolved, resolver(nil), nil, json.RawMessage(args))
}

func readsOf(reqs []string, prefix string) []string {
	var out []string
	for _, r := range reqs {
		if strings.HasPrefix(r, prefix) {
			out = append(out, r)
		}
	}
	return out
}

// TestFileListsFilterEveryRowByItsPage lists without page_id on a connection with exclusions only: the tool
// stays available and each row is kept by the page it points to, the page's chapter, book, and shelf.
func TestFileListsFilterEveryRowByItsPage(t *testing.T) {
	tools := []string{commentsList.ID, attachmentsList.ID, imagesList.ID}
	// Pages: 111 and 555 in chapter 1001 of book 701 (shelf 301), 333 loose in 701, 222 in book 901 chapter 2001.
	cases := []struct {
		name     string
		excludes []string
		comments []int64
		attach   []int64
		images   []int64
		shelfGet bool
	}{
		{"page", []string{"page/555"}, []int64{71, 72, 73, 74, 76}, []int64{81, 83}, []int64{91, 92, 93, 96}, false},
		{"chapter", []string{"chapter/1001"}, []int64{72, 73, 74}, []int64{83}, []int64{92, 93}, false},
		{"book", []string{"book/701"}, []int64{72, 74}, []int64{83}, []int64{92}, false},
		{"shelf", []string{"shelf/301"}, []int64{72, 74}, []int64{83}, []int64{92}, true},
		{"page of the other book", []string{"page/222"}, []int64{71, 73, 74, 75, 76}, []int64{81, 82}, []int64{91, 93, 95, 96}, false},
	}
	for _, tt := range cases {
		for i, tool := range tools {
			want := [][]int64{tt.comments, tt.attach, tt.images}[i]
			s := newFileServer(t)
			got, err := s.callExcluding(t, tool, `{}`, nil, tt.excludes)
			if err != nil {
				t.Errorf("%s %s: %v", tt.name, tool, err)
				continue
			}
			ids := resultIDs(t, got)
			// A row whose page cannot be proven is left out, an image without a page always.
			if !reflect.DeepEqual(ids, want) {
				t.Errorf("%s %s: ids = %v, want %v", tt.name, tool, ids, want)
			}
			reqs := s.requests()
			if n := len(readsOf(reqs, "GET /api/shelves/")); tt.shelfGet && n != 1 || !tt.shelfGet && n != 0 {
				t.Errorf("%s %s: shelf reads = %d in %v", tt.name, tool, n, reqs)
			}
			seen := map[string]bool{}
			for _, r := range readsOf(reqs, "GET /api/pages/") {
				if seen[r] {
					t.Errorf("%s %s: %s read twice", tt.name, tool, r)
				}
				seen[r] = true
			}
		}
	}
}

// TestFileListsReadNoPageWhenOnlyPagesAreExcluded keeps the cost of a plain page exclusion at the listing.
func TestFileListsReadNoPageWhenOnlyPagesAreExcluded(t *testing.T) {
	for _, tool := range []string{commentsList.ID, attachmentsList.ID, imagesList.ID} {
		s := newFileServer(t)
		if _, err := s.callExcluding(t, tool, `{}`, nil, []string{"page/555"}); err != nil {
			t.Fatal(err)
		}
		if got := s.requests(); len(got) != 1 {
			t.Errorf("%s: requests = %v, want the listing only", tool, got)
		}
	}
}

// TestFileListsWithoutExclusionsStayAsTheyWere checks that a connection without targets and exclusions reads no page
// and keeps every row, also an image without a page.
func TestFileListsWithoutExclusionsStayAsTheyWere(t *testing.T) {
	cases := map[string]int{commentsList.ID: 6, attachmentsList.ID: 3, imagesList.ID: 6}
	for tool, n := range cases {
		s := newFileServer(t)
		got, err := s.callExcluding(t, tool, `{}`, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(resultIDs(t, got)) != n || len(s.requests()) != 1 {
			t.Errorf("%s: rows or requests changed", tool)
		}
	}
}

func TestFileListsRefuseAnExcludedPageBeforeAnyRequest(t *testing.T) {
	for _, tool := range []string{commentsList.ID, attachmentsList.ID, imagesList.ID} {
		s := newFileServer(t)
		_, err := s.callExcluding(t, tool, `{"page_id":555}`, nil, []string{"page/555"})
		refused(t, tool, err)
		noExclusionWord(t, tool, err)
		if got := s.requests(); len(got) != 0 {
			t.Errorf("%s: requests = %v, want none", tool, got)
		}
		// A page below an excluded chapter is proven by one read of the page.
		_, err = s.callExcluding(t, tool, `{"page_id":111}`, nil, []string{"chapter/1001"})
		refused(t, tool, err)
		if got, want := s.requests(), []string{"GET /api/pages/111"}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s: requests = %v, want %v", tool, got, want)
		}
	}
}

func TestFileListWithAPageReachesRowsOfThatPageOnly(t *testing.T) {
	s := newFileServer(t)
	got, err := s.callExcluding(t, attachmentsList.ID, `{"page_id":222}`, nil, []string{"chapter/1001"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{83}; !reflect.DeepEqual(resultIDs(t, got), want) {
		t.Errorf("ids = %v, want %v", resultIDs(t, got), want)
	}
	if got, want := s.requests(), []string{"GET /api/pages/222", "GET /api/attachments"}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestFileRowsOfAnUnreadableShelfFailClosed(t *testing.T) {
	s := newFileServer(t)
	for _, tool := range []string{commentsList.ID, attachmentsList.ID, imagesList.ID} {
		_, err := s.callExcluding(t, tool, `{}`, nil, []string{"shelf/398"})
		refused(t, tool, err)
	}
}

type singleCase struct {
	name     string
	tool     string
	args     string
	excludes []string
	reqs     []string
	refuse   bool
}

func runSingleCases(t *testing.T, cases []singleCase) {
	t.Helper()
	dir := t.TempDir()
	for _, tt := range cases {
		s := newFileServer(t)
		args := strings.ReplaceAll(tt.args, "DIR", filepath.ToSlash(dir))
		_, err := s.fileCall(t, tt.tool, args, dir, nil, tt.excludes)
		if tt.refuse {
			refused(t, tt.name, err)
			noExclusionWord(t, tt.name, err)
		} else if err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
		got := s.requests()
		if !reflect.DeepEqual(got, tt.reqs) && !(len(got) == 0 && len(tt.reqs) == 0) {
			t.Errorf("%s: requests = %v, want %v", tt.name, got, tt.reqs)
		}
	}
}

func TestCommentsRefuseExcludedPages(t *testing.T) {
	runSingleCases(t, []singleCase{
		{"get on an excluded page", commentsGet.ID, `{"id":75}`, []string{"page/555"}, []string{"GET /api/comments/75"}, true},
		{"get below an excluded chapter", commentsGet.ID, `{"id":71}`, []string{"chapter/1001"},
			[]string{"GET /api/comments/71", "GET /api/pages/111"}, true},
		{"get below an excluded shelf", commentsGet.ID, `{"id":71}`, []string{"shelf/301"},
			[]string{"GET /api/comments/71", "GET /api/pages/111", "GET /api/shelves/301"}, true},
		{"get beside the exclusion", commentsGet.ID, `{"id":73}`, []string{"chapter/1001"},
			[]string{"GET /api/comments/73", "GET /api/pages/333"}, false},
		{"update on an excluded page", commentsUpdate.ID, `{"id":75,"html":"<p>x</p>"}`, []string{"page/555"},
			[]string{"GET /api/comments/75"}, true},
		{"delete on an excluded page", commentsDelete.ID, `{"id":75}`, []string{"page/555"},
			[]string{"GET /api/comments/75"}, true},
		{"delete beside the exclusion", commentsDelete.ID, `{"id":73}`, []string{"page/555"},
			[]string{"GET /api/comments/73", "DELETE /api/comments/73"}, false},
		{"create on an excluded page", commentsCreate.ID, `{"page_id":555,"html":"<p>x</p>"}`, []string{"page/555"}, nil, true},
		{"create below an excluded chapter", commentsCreate.ID, `{"page_id":111,"html":"<p>x</p>"}`, []string{"chapter/1001"},
			[]string{"GET /api/pages/111"}, true},
		{"create beside the exclusion", commentsCreate.ID, `{"page_id":333,"html":"<p>x</p>"}`, []string{"chapter/1001"},
			[]string{"GET /api/pages/333", "POST /api/comments"}, false},
	})
}

func TestAttachmentsRefuseExcludedPages(t *testing.T) {
	runSingleCases(t, []singleCase{
		{"get on an excluded page", attachmentsGet.ID, `{"id":82}`, []string{"page/555"}, []string{"GET /api/attachments/82"}, true},
		{"get below an excluded book", attachmentsGet.ID, `{"id":82}`, []string{"book/701"},
			[]string{"GET /api/attachments/82", "GET /api/pages/555"}, true},
		{"get beside the exclusion", attachmentsGet.ID, `{"id":83}`, []string{"book/701"},
			[]string{"GET /api/attachments/83", "GET /api/pages/222"}, false},
		{"download on an excluded page", attachmentsDownload.ID, `{"id":82,"local_path":"DIR/out.bin"}`, []string{"page/555"},
			[]string{"GET /api/attachments/82"}, true},
		{"link on an excluded page", attachmentsLink.ID, `{"page_id":555,"name":"n","link":"https://x.example/a"}`,
			[]string{"page/555"}, nil, true},
		{"link below an excluded chapter", attachmentsLink.ID, `{"page_id":111,"name":"n","link":"https://x.example/a"}`,
			[]string{"chapter/1001"}, []string{"GET /api/pages/111"}, true},
		{"link beside the exclusion", attachmentsLink.ID, `{"page_id":333,"name":"n","link":"https://x.example/a"}`,
			[]string{"chapter/1001"}, []string{"GET /api/pages/333", "POST /api/attachments"}, false},
		{"update on an excluded page", attachmentsUpdate.ID, `{"id":82,"name":"n"}`, []string{"page/555"},
			[]string{"GET /api/attachments"}, true},
		{"update to an excluded page", attachmentsUpdate.ID, `{"id":83,"page_id":555}`, []string{"page/555"}, nil, true},
		{"update to a page below an excluded chapter", attachmentsUpdate.ID, `{"id":83,"page_id":111}`, []string{"chapter/1001"},
			[]string{"GET /api/attachments", "GET /api/pages/222", "GET /api/pages/111"}, true},
		{"delete on an excluded page", attachmentsDelete.ID, `{"id":82}`, []string{"page/555"},
			[]string{"GET /api/attachments"}, true},
		{"delete beside the exclusion", attachmentsDelete.ID, `{"id":81}`, []string{"page/555"},
			[]string{"GET /api/attachments", "DELETE /api/attachments/81"}, false},
	})
}

func TestAttachmentUploadAndReplaceRefuseExcludedPages(t *testing.T) {
	dir, path := localFile(t, "data")
	up := jsonArgs(t, map[string]any{"page_id": 555, "name": "n", "local_path": path})
	upBelow := jsonArgs(t, map[string]any{"page_id": 111, "name": "n", "local_path": path})
	replace := jsonArgs(t, map[string]any{"id": 82, "local_path": path})
	cases := []singleCase{
		{"upload on an excluded page", attachmentsUpload.ID, up, []string{"page/555"}, nil, true},
		{"upload below an excluded chapter", attachmentsUpload.ID, upBelow, []string{"chapter/1001"}, []string{"GET /api/pages/111"}, true},
		{"replace on an excluded page", attachmentsReplace.ID, replace, []string{"page/555"}, []string{"GET /api/attachments"}, true},
	}
	for _, tt := range cases {
		s := newFileServer(t)
		_, err := s.fileCall(t, tt.tool, tt.args, dir, nil, tt.excludes)
		refused(t, tt.name, err)
		if got := s.requests(); !reflect.DeepEqual(got, tt.reqs) && !(len(got) == 0 && len(tt.reqs) == 0) {
			t.Errorf("%s: requests = %v, want %v", tt.name, got, tt.reqs)
		}
	}
}

func TestImagesRefuseExcludedPages(t *testing.T) {
	dir, _ := localFile(t, "data")
	png := filepath.Join(dir, "a.png")
	if err := os.WriteFile(png, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	up := func(page int) string {
		return jsonArgs(t, map[string]any{"page_id": page, "type": "gallery", "local_path": png})
	}
	cases := []singleCase{
		{"get on an excluded page", imagesGet.ID, `{"id":95}`, []string{"page/555"}, []string{"GET /api/image-gallery/95"}, true},
		{"get below an excluded chapter", imagesGet.ID, `{"id":91}`, []string{"chapter/1001"},
			[]string{"GET /api/image-gallery/91", "GET /api/pages/111"}, true},
		{"get beside the exclusion", imagesGet.ID, `{"id":93}`, []string{"chapter/1001"},
			[]string{"GET /api/image-gallery/93", "GET /api/pages/333"}, false},
		{"get without a page", imagesGet.ID, `{"id":94}`, []string{"page/555"}, []string{"GET /api/image-gallery/94"}, true},
		{"download on an excluded page", imagesDownload.ID, `{"id":95,"local_path":"DIR/out.png"}`, []string{"page/555"},
			[]string{"GET /api/image-gallery/95"}, true},
		{"update on an excluded page", imagesUpdate.ID, `{"id":95,"name":"n"}`, []string{"page/555"},
			[]string{"GET /api/image-gallery/95"}, true},
		{"replace on an excluded page", imagesReplace.ID, jsonArgs(t, map[string]any{"id": 95, "local_path": png}), []string{"page/555"},
			[]string{"GET /api/image-gallery/95"}, true},
		{"delete on an excluded page", imagesDelete.ID, `{"id":95}`, []string{"page/555"},
			[]string{"GET /api/image-gallery/95"}, true},
		{"delete beside the exclusion", imagesDelete.ID, `{"id":93}`, []string{"page/555"},
			[]string{"GET /api/image-gallery/93", "DELETE /api/image-gallery/93"}, false},
		{"upload to an excluded page", imagesUpload.ID, up(555), []string{"page/555"}, nil, true},
		{"upload below an excluded chapter", imagesUpload.ID, up(111), []string{"chapter/1001"}, []string{"GET /api/pages/111"}, true},
		{"upload beside the exclusion", imagesUpload.ID, up(333), []string{"chapter/1001"},
			[]string{"GET /api/pages/333", "POST /api/image-gallery"}, false},
	}
	for _, tt := range cases {
		s := newFileServer(t)
		args := strings.ReplaceAll(tt.args, "DIR", filepath.ToSlash(dir))
		_, err := s.fileCall(t, tt.tool, args, dir, nil, tt.excludes)
		if tt.refuse {
			refused(t, tt.name, err)
			noExclusionWord(t, tt.name, err)
		} else if err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
		if got := s.requests(); !reflect.DeepEqual(got, tt.reqs) && !(len(got) == 0 && len(tt.reqs) == 0) {
			t.Errorf("%s: requests = %v, want %v", tt.name, got, tt.reqs)
		}
	}
}

func TestImportRunProvesItsParent(t *testing.T) {
	args := func(id int, parentType string, parent int) string {
		return jsonArgs(t, map[string]any{"id": id, "parent_type": parentType, "parent_id": parent})
	}
	runSingleCases(t, []singleCase{
		{"book excluded by itself", importsRun.ID, args(5, "book", 701), []string{"book/701"}, nil, true},
		{"chapter excluded by itself", importsRun.ID, args(6, "chapter", 1001), []string{"chapter/1001"}, nil, true},
		{"book on an excluded shelf", importsRun.ID, args(6, "book", 701), []string{"shelf/301"},
			[]string{"GET /api/imports/6", "GET /api/shelves/301"}, true},
		{"chapter in an excluded book", importsRun.ID, args(5, "book", 701), []string{"book/701"}, nil, true},
		{"chapter in an excluded book by read", importsRun.ID, args(6, "chapter", 1001), []string{"book/701"},
			[]string{"GET /api/imports/6", "GET /api/chapters/1001"}, true},
		{"chapter in a book on an excluded shelf", importsRun.ID, args(6, "chapter", 1001), []string{"shelf/301"},
			[]string{"GET /api/imports/6", "GET /api/chapters/1001", "GET /api/shelves/301"}, true},
		{"book beside the exclusion", importsRun.ID, args(5, "book", 901), []string{"book/701"},
			[]string{"GET /api/imports/5", "POST /api/imports/5"}, false},
		{"book of a chapter import is checked too", importsRun.ID, args(6, "book", 901), []string{"book/701"},
			[]string{"GET /api/imports/6", "POST /api/imports/6"}, false},
		{"chapter beside the exclusion", importsRun.ID, args(6, "chapter", 2001), []string{"chapter/1001"},
			[]string{"GET /api/imports/6", "POST /api/imports/6"}, false},
		{"new book without a parent", importsRun.ID, `{"id":7}`, []string{"book/701", "shelf/301"},
			[]string{"GET /api/imports/7", "POST /api/imports/7"}, false},
	})
}

func TestImportsOtherToolsStayOpenWithExclusions(t *testing.T) {
	for _, tool := range []string{importsList.ID, importsGet.ID, importsDelete.ID} {
		reg := capability.NewRegistry()
		if err := Register(reg); err != nil {
			t.Fatal(err)
		}
		ok, _ := reaches(tool, nil)
		if !ok {
			t.Errorf("%s: not offered", tool)
		}
	}
	s := newFileServer(t)
	if _, err := s.callExcluding(t, importsGet.ID, `{"id":5}`, nil, []string{"book/701", "shelf/301", "page/1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.callExcluding(t, importsDelete.ID, `{"id":5}`, nil, []string{"book/701"}); err != nil {
		t.Fatal(err)
	}
}
