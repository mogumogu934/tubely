package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
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

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid video ID", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User must be video owner", err)
		return
	}

	const maxMemory = 1 << 30 // 1 GB
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

	mpFile, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get file data", err)
		return
	}
	defer mpFile.Close()

	mType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse media type", err)
		return
	}

	if mType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Media type must be video/mp4", err)
		return
	}

	f, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create temp file", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if _, err = io.Copy(f, mpFile); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't copy mp file to temp file", err)
		return
	}

	if _, err = f.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't reset temp file pointer", err)
		return
	}

	fsVideo, err := processVideoForFastStart(f.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't process video for fast start", err)
		return
	}

	fsf, err := os.Open(fsVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't open fast start video for reading", err)
		return
	}
	defer fsf.Close()

	aspRatio, err := getVideoAspectRatio(fsVideo)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get video aspect ratio", err)
		return
	}

	key := make([]byte, 32)
	rand.Read(key)
	objKey := fmt.Sprintf("%s/%s.%s", aspRatio, base64.RawURLEncoding.EncodeToString(key), strings.Split(mType, "/")[1])

	fmt.Println("uploading video", objKey, "by user", userID)

	params := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &objKey,
		Body:        fsf,
		ContentType: &mType,
	}

	if _, err = cfg.s3Client.PutObject(context.Background(), &params); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't add object into S3", err)
		return
	}

	s3URL := fmt.Sprintf("%s,%s", cfg.s3Bucket, objKey)
	video.VideoURL = &s3URL
	if err = cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video in db", err)
		return
	}

	signedVideo, err := cfg.dbVideoToSignedVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't sign video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, signedVideo)
}

func processVideoForFastStart(filePath string) (string, error) {
	path := filePath + ".processing"
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", path)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.Printf("Couldn't run ffmpeg: %v, stderr: %s", err, stderr.String())
		return "", err
	}

	return path, nil
}

func getVideoAspectRatio(filePath string) (string, error) {
	type VideoInfo struct {
		Streams []struct {
			Width  int `json:"width,omitempty"`
			Height int `json:"height,omitempty"`
		} `json:"streams"`
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Printf("File does not exist: %s", filePath)
		return "", err
	}

	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		log.Printf("Couldn't run ffprobe: %v, stderr: %s", err, stderr.String())
		return "", err
	}

	data := VideoInfo{}
	if err := json.Unmarshal(stdout.Bytes(), &data); err != nil {
		log.Printf("Couldn't unmarshal stdout: %v", err)
		return "", err
	}

	if len(data.Streams) == 0 {
		log.Printf("Couldn't get video info")
		return "", nil
	}

	aspRatio := "other"
	result := float64(data.Streams[0].Width) / float64(data.Streams[0].Height)
	if result >= 1.76 && result <= 1.78 {
		aspRatio = "landscape"
	} else if result >= 0.55 && result <= 0.57 {
		aspRatio = "portrait"
	}

	return aspRatio, nil
}
