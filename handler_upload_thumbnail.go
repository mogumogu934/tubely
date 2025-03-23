package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	const maxMemory = 10 << 20
	if err = r.ParseMultipartForm(maxMemory); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error parsing response", err)
		return
	}

	mpFile, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error getting file data", err)
		return
	}
	defer mpFile.Close()

	vid, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error getting video metadata", err)
		return
	}

	if vid.UserID != userID {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	mType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error parsing media type", err)
		return
	}

	if mType != "image/jpeg" && mType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Media type must be jpeg or png", nil)
		return
	}

	key := make([]byte, 32)
	rand.Read(key)
	base64String := base64.RawURLEncoding.EncodeToString(key)
	mExt := "." + strings.Split(mType, "/")[1]

	path := filepath.Join(cfg.assetsRoot, base64String+mExt)
	file, err := os.Create(path)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error saving file", err)
		return
	}

	_, err = io.Copy(file, mpFile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error copying mp file", err)
		return
	}

	tnURL := fmt.Sprintf("http://localhost:%s/assets/%s%s", cfg.port, base64String, mExt)
	vid.ThumbnailURL = &tnURL

	if err = cfg.db.UpdateVideo(vid); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating thumbnail", err)
		return
	}

	vidUpdated, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error getting video metadata", err)
		return
	}

	respondWithJSON(w, http.StatusOK, vidUpdated)
}
