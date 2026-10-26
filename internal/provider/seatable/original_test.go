package seatable

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// wantOriginal checks that err keeps the provider's statement apart from Qatlas' message.
func wantOriginal(t *testing.T, err error, status int, message string) {
	t.Helper()
	var failure *provider.Error
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v, want a provider error", err)
	}
	if message != "" && strings.Contains(err.Error(), message) {
		t.Errorf("the message carries the provider text: %v", err)
	}
	original := failure.Original
	if original == nil || original.Status != status {
		t.Fatalf("original = %+v, want status %d", original, status)
	}
	if message == "" {
		if len(original.Entries) != 0 {
			t.Errorf("entries = %+v, want none", original.Entries)
		}
		return
	}
	if len(original.Entries) != 1 || original.Entries[0].Message != message {
		t.Errorf("entries = %+v, want message %q", original.Entries, message)
	}
}

func TestStatusBranchesKeepTheProviderStatement(t *testing.T) {
	for _, status := range []int{
		http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusBadRequest,
		http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusGatewayTimeout,
		http.StatusInternalServerError,
	} {
		serveBase(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == metaRoute(salesBase) {
				return jsonResponse(http.StatusOK, searchMetadata), nil
			}
			return jsonResponse(status, `{"error_msg":"`+bodyCanary+`"}`), nil
		})
		c, _ := client(t, "Kunden")
		_, err := c.SearchRows(context.Background(), SearchOptions{})
		if err == nil {
			t.Fatalf("status %d: no error", status)
		}
		wantOriginal(t, err, status, bodyCanary)
	}
}

func TestSnapshotRefusalKeepsTheProviderStatement(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusConflict, http.StatusUnprocessableEntity} {
		serveBatch(t, status, `{"error_msg":"`+providerCanary+`"}`)
		err := linksClient(t, "Kunden").CreateSnapshot(context.Background())
		if err == nil {
			t.Fatalf("status %d: no error", status)
		}
		wantOriginal(t, err, status, providerCanary)
	}
}

func TestChangeKeepsTheProviderStatement(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusGatewayTimeout} {
		serveBase(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == metaRoute(salesBase) {
				return jsonResponse(http.StatusOK, fileMetadata), nil
			}
			return jsonResponse(status, `{"error_msg":"`+bodyCanary+`"}`), nil
		})
		stubLimiter(t, salesToken)
		c, _ := client(t, "Kunden")
		err := c.DeleteFile(context.Background(), fileInput(t, `,"index":0`))
		if err == nil {
			t.Fatalf("status %d: no error", status)
		}
		wantOriginal(t, err, status, bodyCanary)
	}
}

func TestCommentNotAppliedKeepsTheProviderStatement(t *testing.T) {
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == metaRoute(salesBase):
			return jsonResponse(http.StatusOK, linkMetadata), nil
		case strings.HasSuffix(request.URL.Path, rowsPath+rowID+"/"):
			return jsonResponse(http.StatusOK, `{"_id":"`+rowID+`","Name":"Bike"}`), nil
		}
		return jsonResponse(http.StatusOK, `{"success":false,"error_msg":"`+bodyCanary+`"}`), nil
	})
	c, _ := client(t, "*")
	err := c.CreateComment(context.Background(), CommentInput{Table: "Kunden", RowID: rowID, Comment: "hi"})
	if err == nil {
		t.Fatal("a refused comment was accepted")
	}
	wantOriginal(t, err, http.StatusOK, bodyCanary)
}

func TestUploadAndDownloadStatements(t *testing.T) {
	fake := newFileFake()
	fake.postStatus = http.StatusForbidden
	serveFiles(t, fake)
	c, _ := client(t, "Kunden")
	_, err := c.UploadFile(context.Background(), fileInput(t, ""), "x.txt", 5, strings.NewReader("hello"))
	if err == nil {
		t.Fatal("a refused upload was accepted")
	}
	// The upload endpoint belongs to SeaTable itself: its "error" field is the provider's statement.
	wantOriginal(t, err, http.StatusForbidden, bodyCanary)

	// The signed download address belongs to the file store: only the status is kept.
	serveBase(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Path == metaRoute(salesBase):
			return jsonResponse(http.StatusOK, fileMetadata), nil
		case request.URL.Path == rowRoute(salesBase, rowID):
			return jsonResponse(http.StatusOK, `{"_id":"`+rowID+`","Anhang":[`+existingFile+`]}`), nil
		case request.URL.Path == downloadLinkPath:
			return jsonResponse(http.StatusOK, `{"download_link":"`+downloadAddress+`"}`), nil
		}
		return jsonResponse(http.StatusNotFound, `{"error":"`+bodyCanary+`"}`), nil
	})
	stubLimiter(t, salesToken)
	c, _ = client(t, "Kunden")
	_, err = c.DownloadFile(context.Background(), fileInput(t, `,"index":0`), nil)
	if err == nil {
		t.Fatal("a refused download was accepted")
	}
	wantOriginal(t, err, http.StatusNotFound, "")
	if strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("the message carries the provider text: %v", err)
	}
}
