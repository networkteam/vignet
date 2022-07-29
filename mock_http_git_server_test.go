package vignet_test

import (
	"fmt"
	"net/http"

	"github.com/apex/log"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitHttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
)

type mockHttpGitServer struct {
	srv transport.Transport
	mux http.Handler
}

func (m *mockHttpGitServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mux.ServeHTTP(w, r)
}

var _ http.Handler = &mockHttpGitServer{}

type mockHttpGitServerOpts struct {
	basicAuth *gitHttp.BasicAuth
}

func newMockHttpGitServer(fs billy.Filesystem, opts mockHttpGitServerOpts) *mockHttpGitServer {
	ld := server.NewFilesystemLoader(fs)
	srv := server.NewServer(ld)

	s := &mockHttpGitServer{
		srv: srv,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/info/refs", s.httpInfoRefs)
	mux.HandleFunc("/git-upload-pack", s.httpGitUploadPack)
	mux.HandleFunc("/git-receive-pack", s.httpGitReceivePack)
	s.mux = mux

	return s
}

func (m *mockHttpGitServer) httpInfoRefs(rw http.ResponseWriter, r *http.Request) {
	log.Debugf("Request httpInfoRefs %s %s", r.Method, r.URL)

	service := r.URL.Query().Get("service")
	if service != "git-upload-pack" && service != "git-receive-pack" {
		http.Error(rw, "Smart Git is required", http.StatusForbidden)
		return
	}

	rw.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-advertisement", service))

	ep, err := transport.NewEndpoint("/")
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to create endpoint")
		return
	}

	var sess transport.Session

	if service == "git-upload-pack" {
		sess, err = m.srv.NewUploadPackSession(ep, nil)
		if err != nil {
			http.Error(rw, "Internal server error", http.StatusInternalServerError)
			log.WithError(err).Error("Failed to create upload pack session")
			return
		}
	} else {
		sess, err = m.srv.NewReceivePackSession(ep, nil)
		if err != nil {
			http.Error(rw, "Internal server error", http.StatusInternalServerError)
			log.WithError(err).Error("Failed to create receive pack session")
			return
		}
	}
	defer sess.Close()

	ar, err := sess.AdvertisedReferencesContext(r.Context())
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to get advertised references")
		return
	}
	ar.Prefix = [][]byte{
		[]byte(fmt.Sprintf("# service=%s", service)),
		pktline.Flush,
	}
	err = ar.Encode(rw)
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to encode advertised references")
		return
	}
}

func (m *mockHttpGitServer) httpGitUploadPack(rw http.ResponseWriter, r *http.Request) {
	log.Debugf("Request httpGitUploadPack %s %s", r.Method, r.URL)

	rw.Header().Set("Content-Type", "application/x-git-upload-pack-result")

	upr := packp.NewUploadPackRequest()
	err := upr.Decode(r.Body)
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to decode upload pack request")
		return
	}

	ep, err := transport.NewEndpoint("/")
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to create endpoint")
		return
	}
	sess, err := m.srv.NewUploadPackSession(ep, nil)
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to create upload pack session")
		return
	}
	defer sess.Close()

	res, err := sess.UploadPack(r.Context(), upr)
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to upload pack")
		return
	}
	defer res.Close()

	err = res.Encode(rw)
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to encode upload pack response")
		return
	}

}

func (m *mockHttpGitServer) httpGitReceivePack(rw http.ResponseWriter, r *http.Request) {
	log.Debugf("Request httpGitReceivePack %s %s", r.Method, r.URL)

	rw.Header().Set("Content-Type", "application/x-git-receive-pack-result")

	upr := packp.NewReferenceUpdateRequest()
	err := upr.Decode(r.Body)
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to decode reference update request")
		return
	}

	ep, err := transport.NewEndpoint("/")
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to create endpoint")
		return
	}

	sess, err := m.srv.NewReceivePackSession(ep, nil)
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to create receive pack session")
		return
	}
	defer sess.Close()

	res, err := sess.ReceivePack(r.Context(), upr)
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to receive pack")
		return
	}

	err = res.Encode(rw)
	if err != nil {
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		log.WithError(err).Error("Failed to encode receive pack response")
		return
	}
}
