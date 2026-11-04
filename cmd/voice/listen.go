package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"voice/internal/listen"
	"voice/internal/service"
)

func runListen(args []string) error {
	fs, cfgPath := flags("listen")
	wait := fs.Duration("wait", 15*time.Second, "give up if no speech starts within this")
	maxSpeech := fs.Duration("max", 30*time.Second, "stop after this much speech")
	asJSON := fs.Bool("json", false, "print the full result as JSON")
	prompt := fs.String("prompt", "", "say this first")
	fs.Parse(args)
	if _, err := loadConfig(*cfgPath, ""); err != nil {
		return err
	}
	// The background process records, so no voice talks into the microphone
	// and the recognizer stays loaded between calls.
	raw, err := newClient(*cfgPath).Listen(context.Background(), service.ListenArgs{Prompt: *prompt, WaitSeconds: wait.Seconds(), MaxSeconds: maxSpeech.Seconds()})
	if err != nil {
		return err
	}
	b, _ := json.Marshal(raw)
	if *asJSON {
		fmt.Println(string(b))
		return nil
	}
	var res listen.Result
	json.Unmarshal(b, &res)
	if res.Text == "" {
		return errors.New(res.Reason)
	}
	fmt.Println(res.Text)
	return nil
}

func runTranscribe(args []string) error {
	fs, cfgPath := flags("transcribe")
	model := fs.String("model", "", "ASR model directory (default: [listen] model)")
	fs.Parse(args)
	if fs.NArg() == 0 {
		return errors.New("usage: voice transcribe [-model dir] file.wav...")
	}
	cfg, err := loadConfig(*cfgPath, "")
	if err != nil {
		return err
	}
	if *model != "" {
		cfg.Listen.Model = *model
	}
	l := listen.New(cfg)
	defer l.Close()
	for _, f := range fs.Args() {
		start := time.Now()
		text, lang, err := l.TranscribeFile(f)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		fmt.Fprintf(os.Stderr, "%s: %.2fs %s\n", f, time.Since(start).Seconds(), lang)
		fmt.Println(text)
	}
	return nil
}
