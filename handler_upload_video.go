package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
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
	const maxMemory = 1 << 30
	file, _, err := r.FormFile("video")
	guile := http.MaxBytesReader(w, file, maxMemory)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()
	vid, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get video file", err)
		return
	}
	if vid.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Not your video,is it punk?", err)
		return
	}
	mediatype, _, err := mime.ParseMediaType("video/mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to parse mediatype", err)
		return
	}
	tile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create temporary file", err)
		return
	}
	defer os.Remove("tubely-upload.mp4")
	defer tile.Close()
	_, err = io.Copy(tile, guile)

	tile.Seek(0, io.SeekStart)
	slicebyte := make([]byte, 32)
	_, err = rand.Read(slicebyte)
	bytesized := base64.RawURLEncoding.EncodeToString(slicebyte)
	key := fmt.Sprintf("%v.%v", bytesized, strings.TrimPrefix(mediatype, "video/"))

	putobjparams := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        tile,
		ContentType: &mediatype,
	}
	_, err = cfg.s3Client.PutObject(context.Background(), &putobjparams)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to put object into AWS", err)
		return
	}
	new_url := fmt.Sprintf("https://%v.s3.%v.amazonaws.com/%v", cfg.s3Bucket, cfg.s3Region, key)
	vid.VideoURL = &new_url
	err = cfg.db.UpdateVideo(vid)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update video in database", err)
		return
	}
	respondWithJSON(w, http.StatusOK, vid)
}
