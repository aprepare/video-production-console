package videoqc

import (
	"math"
	"sort"
)

// Base samples cover every second, or the whole duration evenly. Remaining
// capacity is assigned to pairs around real scene cuts (not synthetic shots).
func sampleFrames(duration, fps float64, cuts []float64) []Frame {
	if !positiveFinite(duration) {
		return nil
	}
	if !positiveFinite(fps) {
		fps = 25
	}
	step := 1 / fps
	last := math.Max(0, duration-step)
	times := []float64{}
	if duration <= 30 {
		for i := 0; i < int(math.Ceil(duration)); i++ {
			times = append(times, math.Min(float64(i), last))
		}
	} else {
		for i := 0; i < 30; i++ {
			times = append(times, last*float64(i)/29)
		}
	}
	ordered := append([]float64(nil), cuts...)
	sort.Float64s(ordered)
	validCuts := []float64{}
	for _, cut := range ordered {
		if !positiveFinite(cut) || cut >= duration {
			continue
		}
		if len(validCuts) == 0 || math.Abs(cut-validCuts[len(validCuts)-1]) > 1e-7 {
			validCuts = append(validCuts, cut)
		}
	}
	// Choose cut pairs across the entire video if they exceed the remaining
	// budget; do not fill the budget with only the opening transitions.
	budget := (40 - len(times)) / 2
	if len(validCuts) > budget {
		selected := make([]float64, budget)
		for i := range selected {
			index := len(validCuts) / 2
			if budget > 1 {
				index = int(math.Round(float64(i) * float64(len(validCuts)-1) / float64(budget-1)))
			}
			selected[i] = validCuts[index]
		}
		validCuts = selected
	}
	for _, cut := range validCuts {
		times = append(times, math.Max(0, cut-step), math.Min(last, cut+step))
	}
	sort.Float64s(times)
	frames := []Frame{}
	for _, timestamp := range times {
		if len(frames) > 0 && math.Abs(frames[len(frames)-1].TimeS-timestamp) < 1e-7 {
			continue
		}
		frames = append(frames, Frame{Index: len(frames) + 1, TimeS: timestamp})
	}
	return frames
}
