package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func getThumbName(inExtension string) string {
	var raw []byte = make([]byte, 32)
	num, err := rand.Read(raw)
	if num < len(raw) {
		// this shouldn't really be possible.
		log.Println("getThumbName() rand returned an improbable status!", err)
	}
	dst := make([]byte, base64.RawStdEncoding.EncodedLen(len(raw)))
	base64.RawURLEncoding.Encode(dst, raw)
	return fmt.Sprintf("%s%s", dst, inExtension)
}

func EmbedThumbnail(inData thumbnail) string { // DEPRECATED
	// convert the raw image data to an embedable html data element
	dat := base64.StdEncoding.EncodeToString(inData.data)
	return fmt.Sprintf("data:%s;base64,%s", inData.mediaType, dat)
}

func (cfg *apiConfig) saveFileToDisc(inData thumbnail, fileExtension string) (string, error) {
	dest := getThumbName(fileExtension)
	finalDest := filepath.Join(cfg.assetsRoot, dest)
	_, err := exists(finalDest)
	if err != nil {
		return "", err
	}
	fp, err := os.Create(finalDest)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(fp, bytes.NewReader(inData.data))
	if err != nil {
		return "", err
	}
	return dest, nil
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
	fileMime, _, err := mime.ParseMediaType(formFileHeader.Header.Get("Content-Type"))
	if err != nil {
		log.Println("handlerUploadThumbnail() error getting mime type", err)
		respondWithError(w, http.StatusInternalServerError, "error determining mime type", err)
		return
	}
	if !slices.Contains([]string{"image/png", "image/jpg"}, fileMime) {
		log.Println("handlerUploadThumbnail() incorrect mime type:", fileMime)
		respondWithError(w, http.StatusBadRequest, "Incorrect file type provided", fmt.Errorf("%s is not valid type", fileMime))
		return
	}
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
	fileExtension, err := mime.ExtensionsByType(fileMime)
	if err != nil {
		log.Println("handlerUploadThumbnail() unable to determine mime extension", err)
		respondWithError(w, http.StatusInternalServerError, "unable to determine file extension", err)
		return
	}
	thumb := thumbnail{data: imageData, mediaType: fileMime}
	videoThumbnails[videoID] = thumb
	fName, err := cfg.saveFileToDisc(thumb, fileExtension[0])
	if err != nil {
		log.Println("handlerUploadThumbnail() unable to save file to disc", err)
		respondWithError(w, http.StatusInternalServerError, "Unable to save file to disc", err)
		return
	}
	thumbnailURL := fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, fName)
	dbVideo.ThumbnailURL = &thumbnailURL
	err = cfg.db.UpdateVideo(dbVideo)
	if err != nil {
		log.Println("handlerUploadThumbnail() unable to add thumbnail to database", err)
		respondWithError(w, http.StatusInternalServerError, "Unable to write thumbnail to database", err)
		return
	}
	respondWithJSON(w, http.StatusOK, struct{}{})
}
