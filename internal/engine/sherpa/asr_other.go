//go:build !windows

package sherpa

import "errors"

var errASR = errors.New("sherpa: speech recognition is implemented for Windows only")

type Recognizer struct{ Family string }

func NewRecognizer(string, string, int) (*Recognizer, error)          { return nil, errASR }
func (*Recognizer) Transcribe([]float32, int) (string, string, error) { return "", "", errASR }
func (*Recognizer) Close()                                            {}

type VAD struct{}

const VADWindow = 512

func NewVAD(string, float32) (*VAD, error) { return nil, errASR }
func (*VAD) Accept([]float32)              {}
func (*VAD) Speaking() bool                { return false }
func (*VAD) Flush()                        {}
func (*VAD) Segments() [][]float32         { return nil }
func (*VAD) Close()                        {}
