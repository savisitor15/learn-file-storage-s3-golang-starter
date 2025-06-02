package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, fmt.Errorf("blank url")
	}
	expanded := strings.Split(*video.VideoURL, ",")
	if len(expanded) < 2 {
		return video, fmt.Errorf("unable to parse url")
	}
	bucket := expanded[0]
	key := expanded[1]
	expire := time.Minute * 5
	url, err := generatePresignedURL(cfg.s3Client, bucket, key, expire)
	video.VideoURL = &url
	return video, err

}

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	// 1 Gb of memory
	const maxMemory = 10 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

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
	log.Println("handlerUploadVideo() uploading thumbnail for video", videoID, "by user", userID)
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		log.Println("handlerUploadVideo() error parsing multipart form", err)
		respondWithError(w, http.StatusInternalServerError, "error parsing multipart form", err)
		return
	}
	dbVideo, err := cfg.db.GetVideo(videoID)
	if err != nil {
		log.Println("handlerUploadVideo() unable to find video in database", err)
		respondWithError(w, http.StatusNotFound, "error getting video", err)
		return
	}
	formFile, formFileHeader, err := r.FormFile("video")
	if err != nil {
		log.Println("handlerUploadVideo() error getting video", err)
		respondWithError(w, http.StatusInternalServerError, "error getting video", err)
		return
	}
	defer formFile.Close()
	fileMime, _, err := mime.ParseMediaType(formFileHeader.Header.Get("Content-Type"))
	if err != nil {
		log.Println("handlerUploadVideo() error getting mime type", err)
		respondWithError(w, http.StatusInternalServerError, "error determining mime type", err)
		return
	}
	if !slices.Contains([]string{"video/mp4"}, fileMime) {
		log.Println("handlerUploadVideo() incorrect mime type:", fileMime)
		respondWithError(w, http.StatusBadRequest, "Incorrect file type provided", fmt.Errorf("%s is not valid type", fileMime))
		return
	}
	// Temp file for uploading to S3
	tmpFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		log.Println("handlerUploadVideo() Failed to create a temp file of the upload", err)
		respondWithError(w, http.StatusInternalServerError, "unable to create a temp file", err)
		return
	}
	defer os.Remove(tmpFile.Name())
	_, err = io.Copy(tmpFile, formFile)
	if err != nil {
		log.Println("handlerUploadVideo() Failed to copy data from stream to temp file", err)
		respondWithError(w, http.StatusInternalServerError, "unable to copy into temp file", err)
		return
	}
	aspect, err := getVideoAspectRatio(tmpFile.Name())
	if err != nil {
		log.Println("handlerUploadVideo() failed to get video aspect ratio", err)
		respondWithError(w, http.StatusInternalServerError, "unable to get aspect ratio from temp file", err)
		return
	}
	// reset to start
	tmpFile.Seek(0, io.SeekStart)
	processedFileName, err := processVideoForFastStart(tmpFile.Name())
	if err != nil {
		log.Println("handlerUploadVideo() unable to process temp video", err)
		respondWithError(w, http.StatusInternalServerError, "Unable to process video", err)
		return
	}
	err = tmpFile.Close()
	if err != nil {
		log.Println("handlerUploadVideo() unable to close temp file pointer, please investigate", err)
		log.Println("Continueing...")
	}
	tmpFile, err = os.Open(processedFileName)
	if err != nil {
		log.Println("handlerUploadVideo() unable to open processed file", err)
		respondWithError(w, http.StatusInternalServerError, "Unable to open process video", err)
		return
	}
	defer tmpFile.Close()
	defer os.Remove(processedFileName)
	destName := fmt.Sprintf("%s/%s", aspect, getThumbName(".mp4"))
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{Bucket: &cfg.s3Bucket, Key: &destName, Body: tmpFile, ContentType: &fileMime})
	if err != nil {
		log.Println("handlerUploadVideo() Unable to upload file", err)
		respondWithError(w, http.StatusInternalServerError, "Unable to push to s3", err)
		return
	}
	newURL := fmt.Sprintf("%s,%s", cfg.s3Bucket, destName)
	dbVideo.VideoURL = &newURL
	err = cfg.db.UpdateVideo(dbVideo)
	if err != nil {
		log.Println("handlerUploadVideo() Error updating video record", err)
		respondWithError(w, http.StatusInternalServerError, "Unable to write video to database", err)
		return
	}
	respondWithJSON(w, http.StatusOK, struct{}{})
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s3Client)
	if presignClient == nil {
		log.Println("generatePresignedURL() error creating client!")
		return "", fmt.Errorf("error creating client")
	}
	presignReq, err := presignClient.PresignGetObject(context.Background(), &s3.GetObjectInput{Bucket: &bucket, Key: &key}, s3.WithPresignExpires(expireTime))
	if err != nil {
		log.Println("generatePresignedURL() error gettting presigned request", err)
		return "", err
	}
	return presignReq.URL, nil
}
