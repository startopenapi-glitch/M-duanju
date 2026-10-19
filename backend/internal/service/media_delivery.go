package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Each callback owns its staging directory. Published paths are content addressed
// and created without replacement; a rejected concurrent callback cannot alter any
// path already referenced by a shot, project or immutable settlement receipt.
func (s *Service) stageGenerationArtifact(remoteURL, kind, taskID string, aspect ...string) (string, error) {
	sub, extension := "videos", ".mp4"
	if kind == "image" {
		sub, extension = "keyvisuals", ".png"
	}
	dir := s.storageDir(sub)
	staging, err := os.MkdirTemp(dir, ".generation-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	local := filepath.Join(staging, "source"+extension)
	if err = downloadFile(remoteURL, local); err != nil {
		return "", err
	}
	if err = validateGenerationMedia(local, kind); err != nil {
		return "", err
	}
	if kind == "image" {
		if len(aspect) > 0 {
			local, err = s.normalizeKeyVisualAspect(local, aspect[0])
			if err != nil {
				return "", err
			}
		}
		local, err = s.prepareKeyVisualForUpstream(local)
		if err != nil {
			return "", err
		}
	} else if kind == "video" && len(aspect) > 0 {
		local, err = s.normalizeVideoAspect(local, aspect[0])
		if err != nil {
			return "", err
		}
	}
	digest, err := mediaDigest(local)
	if err != nil {
		return "", err
	}
	final := filepath.Join(dir, generationArtifactKey(taskID)+"-"+digest+filepath.Ext(local))
	if err = publishImmutableMedia(local, final); err != nil {
		return "", err
	}
	return final, nil
}

func validateGenerationMedia(path, kind string) error {
	if kind == "image" {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		decoded, _, err := image.Decode(f)
		if err != nil {
			return fmt.Errorf("invalid generated image: %w", err)
		}
		if decoded.Bounds().Empty() {
			return errors.New("empty generated image")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Counting decoded frames also rejects corrupt chunked responses that have no
	// Content-Length. A container header alone is insufficient for delivery.
	cmd := exec.CommandContext(ctx, getenvDefault("FFPROBE_PATH", "ffprobe"), "-v", "error", "-count_frames", "-show_entries", "stream=codec_type,nb_read_frames", "-of", "json", path)
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	raw, err := cmd.Output()
	if err != nil || diagnostic.Len() > 0 {
		return fmt.Errorf("invalid generated video: %v %s", err, truncate(diagnostic.String(), 200))
	}
	var result struct {
		Streams []struct {
			Type   string `json:"codec_type"`
			Frames string `json:"nb_read_frames"`
		} `json:"streams"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return err
	}
	for _, stream := range result.Streams {
		if stream.Type == "video" && stream.Frames != "" && stream.Frames != "N/A" && stream.Frames != "0" {
			return nil
		}
	}
	return errors.New("generated video has no decoded frames")
}

func mediaDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func publishImmutableMedia(source, destination string) error {
	f, err := os.OpenFile(source, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if err = f.Chmod(0644); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Link(source, destination); err == nil {
		return nil
	}
	if !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(destination)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("immutable media path is not a regular file")
	}
	expected, err := mediaDigest(source)
	if err != nil {
		return err
	}
	actual, err := mediaDigest(destination)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("immutable media content conflict")
	}
	return nil
}
