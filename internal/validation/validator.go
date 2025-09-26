package validation

import "github.com/sabadia/svc-downloader/internal/models"

type NoopValidator struct{}

func (NoopValidator) ValidateRequest(req models.RequestOptions) error   { return nil }
func (NoopValidator) ValidateConfig(cfg models.DownloadConfig) error    { return nil }
func (NoopValidator) ValidateFileOptions(file models.FileOptions) error { return nil }
