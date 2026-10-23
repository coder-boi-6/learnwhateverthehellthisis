package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
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

	// TODO: implement the upload here
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()
	mediatype:=header.Header.Get("Content-Type")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to read from form file", err)
		return
	}
	vid,err:=cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to get video file", err)
		return
	}
	if vid.UserID!=userID{
		respondWithError(w, http.StatusUnauthorized, "Not your video,is it punk?", err)
		return
	}
	
	slicebyte:=make([]byte,32)
	_,err=rand.Read(slicebyte)
	bytesized:=base64.RawURLEncoding.EncodeToString(slicebyte)
	
	oorl:=fmt.Sprintf("%v.%v",bytesized,strings.TrimPrefix(mediatype,"image/"))
	new_oorl:=filepath.Join(cfg.assetsRoot,oorl)
	tile,err:=os.Create(new_oorl)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to create new file", err)
		return
	}
	defer tile.Close()
	new_oorl="http://localhost:"+cfg.port+"/"+new_oorl
	_,err=io.Copy(tile,file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to copy over to new file", err)
		return
	}
	vid.ThumbnailURL=&new_oorl
	
	err=cfg.db.UpdateVideo(vid)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Unable to update video file", err)
		return
	}

	respondWithJSON(w, http.StatusOK, vid)
}
