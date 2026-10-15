package youtube

import (
	"context"
	"encoding/json"
	"fmt"
)

// loudnessReference is YouTube's loudness reference, in LUFS. The player
// response reports a track's loudness as loudnessDb, how far it sits above
// this reference, so the gain that brings it here is -loudnessDb dB.
const loudnessReference = -14.0

// Loudness returns how far a track sits above YouTube's loudness reference,
// in dB, as its player response reports. The request has to speak as the web
// client with a session: the music client answers UNPLAYABLE and anonymous
// requests get no playable answer, so a signed-out client has nothing to
// read, and ok is false.
func (c *Client) Loudness(ctx context.Context, videoID string) (loudnessDb float64, ok bool, err error) {
	raw, err := c.executeForClient(ctx, "player", map[string]any{
		"videoId":        videoID,
		"contentCheckOk": true,
		"racyCheckOk":    true,
	}, webClient)
	if err != nil {
		return 0, false, err
	}
	var response struct {
		PlayabilityStatus struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"playabilityStatus"`
		PlayerConfig struct {
			AudioConfig struct {
				LoudnessDb           *float64 `json:"loudnessDb"`
				PerceptualLoudnessDb *float64 `json:"perceptualLoudnessDb"`
			} `json:"audioConfig"`
		} `json:"playerConfig"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return 0, false, fmt.Errorf("youtube: decode player response: %w", err)
	}
	if response.PlayabilityStatus.Status != "OK" && response.PlayabilityStatus.Status != "" {
		return 0, false, nil
	}
	if db := response.PlayerConfig.AudioConfig.LoudnessDb; db != nil {
		return *db, true, nil
	}
	// Without loudnessDb, the track's measured loudness is still there, and
	// its distance above the reference is the measurement minus the reference.
	if db := response.PlayerConfig.AudioConfig.PerceptualLoudnessDb; db != nil {
		return *db - loudnessReference, true, nil
	}
	return 0, false, nil
}
