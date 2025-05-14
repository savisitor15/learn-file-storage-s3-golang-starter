package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func EmbedThumbnail(inData thumbnail) string {
	// convert the raw image data to an embedable html data element
	dat := base64.StdEncoding.EncodeToString(inData.data)
	return fmt.Sprintf("data:%s;base64,%s", inData.mediaType, dat)
}

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

	// 10 mb of memory
	const maxMemory = 10 << 20
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		log.Println("handlerUploadThumbnail() error parsing multipart form", err)
		respondWithError(w, http.StatusInternalServerError, "error parsing multipart form", err)
		return
	}

	formFile, formFileHeader, err := r.FormFile("thumbnail")
	if err != nil {
		log.Println("handlerUploadThumbnail() error getting thumbnail", err)
		respondWithError(w, http.StatusInternalServerError, "error getting thumbnail", err)
		return
	}
	defer formFile.Close()
	fileMime := formFileHeader.Header.Get("Content-Type")
	imageData, err := io.ReadAll(formFile)
	if err != nil {
		log.Println("handlerUploadThumbnail() error getting data", err)
		respondWithError(w, http.StatusInternalServerError, "error getting data", err)
		return
	}

	dbVideo, err := cfg.db.GetVideo(videoID)
	if err != nil {
		log.Println("handlerUploadThumbnail() unable to find video in database", err)
		respondWithError(w, http.StatusNotFound, "error getting video", err)
		return
	}
	if dbVideo.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "not allowed", nil)
		return
	}
	thumb := thumbnail{data: imageData, mediaType: fileMime}
	videoThumbnails[videoID] = thumb
	thumbnailURL := EmbedThumbnail(thumb)
	dbVideo.ThumbnailURL = &thumbnailURL
	err = cfg.db.UpdateVideo(dbVideo)
	if err != nil {
		log.Println("handlerUploadThumbnail() unable to add thumbnail to database", err)
		respondWithError(w, http.StatusInternalServerError, "Unable to write thumbnail to database", err)
		return
	}
	respondWithJSON(w, http.StatusOK, struct{}{})
}
