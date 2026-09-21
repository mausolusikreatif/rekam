package api

import (
	"net/http"
	"strconv"
)

// maxUploadBytes caps a single image upload. Images are stored inline in the
// tenant's *.files SQLite; the cap keeps blobs small and the store light.
const maxUploadBytes = 2 << 20 // 2 MiB

// allowedUploadMIME is the set of image types accepted by POST /files. SVG is
// deliberately excluded: it can carry scripts, and the preview sanitizer would
// have to special-case it.
var allowedUploadMIME = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
}

// handleUploadFile accepts a multipart image upload and stores it in the
// tenant's file store, returning the id and a /files/<id> URL to embed.
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	key := apiKeyFromRequest(r)

	// Reject oversized bodies before buffering them into memory.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+1024)
	if err := r.ParseMultipartForm(maxUploadBytes + 1024); err != nil {
		respond(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "upload too large (max 2 MiB)",
		})
		return
	}

	file, hdr, err := r.FormFile("file")
	if err != nil {
		respond(w, http.StatusBadRequest, map[string]string{"error": "missing 'file' field"})
		return
	}
	defer file.Close()

	if hdr.Size > maxUploadBytes {
		respond(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "upload too large (max 2 MiB)",
		})
		return
	}

	mime := hdr.Header.Get("Content-Type")
	if _, ok := allowedUploadMIME[mime]; !ok {
		respond(w, http.StatusUnsupportedMediaType, map[string]string{
			"error": "unsupported image type: " + mime,
		})
		return
	}

	data := make([]byte, 0, hdr.Size)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := file.Read(buf)
		data = append(data, buf[:n]...)
		if len(data) > maxUploadBytes {
			respond(w, http.StatusRequestEntityTooLarge, map[string]string{
				"error": "upload too large (max 2 MiB)",
			})
			return
		}
		if rerr != nil {
			break
		}
	}

	id, identityID, err := s.eng.PutFile(key, mime, data)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	// Capability URL: the (identity, file) UUID pair authorizes the read, so the
	// image renders in a plain <img> tag without an Authorization header.
	url := "/files/" + identityID + "/" + id
	respond(w, http.StatusCreated, map[string]string{"id": id, "url": url})
}

// handleGetFile streams a stored blob with its content type and a long,
// immutable cache (blob bytes never change for a given id). Authorization is the
// capability URL itself — the unguessable (identity, file) UUID pair — so no
// Bearer token is required and the image renders in a plain <img> tag.
func (s *Server) handleGetFile(w http.ResponseWriter, r *http.Request) {
	identity := r.PathValue("identity")
	id := r.PathValue("id")

	f, err := s.eng.GetFile(identity, id)
	if err != nil {
		s.respondErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", f.Mime)
	w.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	w.Write(f.Data)
}
