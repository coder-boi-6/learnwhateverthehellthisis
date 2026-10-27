package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
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
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	typemedia := header.Header.Get("Content-Type")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to read from form file", err)
		return
	}
	guile := http.MaxBytesReader(w, file, maxMemory)
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
	mediatype, _, err := mime.ParseMediaType(typemedia)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to parse mediatype", err)
		return
	}
	tile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create temporary file", err)
		return
	}
	defer os.Remove(tile.Name())
	defer tile.Close()
	_, err = io.Copy(tile, guile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to copy file content over", err)
		return
	}

	tile.Seek(0, io.SeekStart)
	slicebyte := make([]byte, 32)
	_, err = rand.Read(slicebyte)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create random 32 bytes for key", err)
		return
	}
	bytesized := base64.RawURLEncoding.EncodeToString(slicebyte)

	ratio, err := getVideoAspectRatio(tile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get video aspect ratio", err)
		return
	}
	switch ratio {
	case "16:9":
		ratio = "landscape"
	case "9:16":
		ratio = "portrait"
	default:
		ratio = "other"
	}
	outputpath, err := processVideoForFastStart(tile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to process video for faststart", err)
		return
	}
	processedtile, err := os.Open(outputpath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to open faststart video", err)
		return
	}
	key := fmt.Sprintf("%v/%v.%v", ratio, bytesized, strings.TrimPrefix(mediatype, "video/"))

	putobjparams := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        processedtile,
		ContentType: &mediatype,
	}
	_, err = cfg.s3Client.PutObject(context.Background(), &putobjparams)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to put object into AWS", err)
		return
	}
	new_url := fmt.Sprintf("%v,%v", cfg.s3Bucket, key)
	vid.VideoURL = &new_url
	err = cfg.db.UpdateVideo(vid)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to update video in database", err)
		return
	}
	updatedvid, err := cfg.dbVideoToSignedVideo(vid)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to sign video", err)
		return
	}
	respondWithJSON(w, http.StatusOK, updatedvid)
}

func getVideoAspectRatio(filePath string) (string, error) {
	command := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	buffr := bytes.Buffer{}
	command.Stdout = &buffr
	err := command.Run()
	if err != nil {
		return "", err
	}
	type aspect_ratio struct {
		Streams []struct {
			Aspect_Ratio string `json:"display_aspect_ratio"`
		} `json:"streams"`
	}
	ratioed := aspect_ratio{}
	err = json.Unmarshal(buffr.Bytes(), &ratioed)
	if err != nil {
		return "", err
	}
	return ratioed.Streams[0].Aspect_Ratio, nil
}

func processVideoForFastStart(filePath string) (string, error) {
	outputpath := filePath + ".processing"
	command := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputpath)
	err := command.Run()
	if err != nil {
		return "", err
	}
	return outputpath, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignclient := s3.NewPresignClient(s3Client)
	presigned, err := presignclient.PresignGetObject(context.Background(), &s3.GetObjectInput{Bucket: &bucket, Key: &key}, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", err
	}
	return presigned.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	url := *video.VideoURL
	slicebucketkey := strings.Split(url, ",")
	bucket := slicebucketkey[0]
	key := slicebucketkey[1]
	newurl, err := generatePresignedURL(cfg.s3Client, bucket, key, time.Minute*5)
	if err != nil {
		return database.Video{}, err
	}
	video.VideoURL = &newurl
	return video, nil
}
