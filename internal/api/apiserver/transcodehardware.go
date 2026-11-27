package apiserver

import (
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// transcodeAvailability is what each engine does for a target's codec; auto is available when anything is.
func (s *Server) transcodeAvailability(videoCodec string) []gen.TranscodeEngineAvailability {
	codec, known := transcode.CodecNamed(videoCodec)
	if s.transcoder == nil || !known {
		return []gen.TranscodeEngineAvailability{{Engine: gen.TranscodeEngineAvailabilityEngine("auto"), Available: true, PresetApplies: true}}
	}
	out := []gen.TranscodeEngineAvailability{{Engine: gen.TranscodeEngineAvailabilityEngine("auto")}}
	for _, engine := range transcode.Engines {
		row, _ := s.transcoder.EncoderFor(engine, codec)
		one := gen.TranscodeEngineAvailability{
			Engine: gen.TranscodeEngineAvailabilityEngine(engine), Available: row.Working, Lookahead: row.Lookahead, TenBit: row.TenBit,
			QualityMode: row.QualityMode, PresetApplies: engine != transcode.VAAPI,
		}
		if !row.Working && row.Detail != "" {
			reason := row.Detail
			one.Reason = &reason
		}
		out = append(out, one)
		out[0].Available = out[0].Available || row.Working
	}
	out[0].PresetApplies = true
	return out
}

func (s *Server) transcodeHardware() gen.TranscodeHardware {
	out := gen.TranscodeHardware{Capabilities: []gen.TranscodeCapability{}, Encoders: []gen.TranscodeEncoderSupport{}, Filters: map[string]bool{}}
	if s.transcoder == nil {
		return out
	}
	out.FfmpegAvailable, out.FfmpegVersion, out.Filters = s.transcoder.Available(), s.transcoder.FFmpegVersion(), s.transcoder.Filters()
	out.CpuCores, out.EncodeEngines = s.transcoder.CPUCores(), s.transcoder.EncodeEngines()
	for _, capability := range s.transcoder.Capabilities() {
		one := gen.TranscodeCapability{Accel: gen.TranscodeCapabilityAccel(capability.Accel), Working: capability.Working}
		if capability.Device != "" {
			one.Device = &capability.Device
		}
		if capability.Name != "" {
			one.Name = &capability.Name
		}
		if capability.Detail != "" {
			one.Detail = &capability.Detail
		}
		if capability.Model != "" {
			one.Model = &capability.Model
		}
		if capability.Engines.Encode > 0 {
			one.EncodeEngines = &capability.Engines.Encode
		}
		if capability.Engines.Decode > 0 {
			one.DecodeEngines = &capability.Engines.Decode
		}
		out.Capabilities = append(out.Capabilities, one)
	}
	for _, row := range s.transcoder.Encoders() {
		one := gen.TranscodeEncoderSupport{
			Engine: gen.TranscodeEncoderSupportEngine(row.Engine), Codec: gen.TranscodeEncoderSupportCodec(row.Codec), Encoder: row.Encoder,
			Working: row.Working, Lookahead: row.Lookahead, TenBit: row.TenBit, QualityMode: row.QualityMode,
		}
		if row.Detail != "" {
			detail := row.Detail
			one.Detail = &detail
		}
		out.Encoders = append(out.Encoders, one)
	}
	return out
}

// GetTranscodeHardware answers the last encoder detection. Administrator only.
func (s *Server) GetTranscodeHardware(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.transcodeHardware())
}

// DetectTranscodeHardware tries every engine again and answers what it found. Administrator only.
func (s *Server) DetectTranscodeHardware(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.transcoder != nil {
		s.transcoder.Detect(r.Context())
		s.transcoder.DetectEncoders(r.Context())
	}
	writeJSON(w, s.logger, http.StatusOK, s.transcodeHardware())
}
