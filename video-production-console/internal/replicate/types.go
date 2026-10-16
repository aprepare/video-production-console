package replicate

import "time"

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string     { return e.Message }
func invalid(message string) error { return &Error{"invalid_replicate", message} }

type CreateRequest struct {
	Blank         bool   `json:"blank"`
	Title         string `json:"title"`
	ReferenceID   string `json:"reference_id"`
	AnalysisID    string `json:"analysis_id"`
	Product       string `json:"product"`
	Ratio         string `json:"ratio"`
	VideoProvider string `json:"video_provider"`
	ImageModel    string `json:"image_model"`
	Variants      int    `json:"variants"`
}
type ProjectPatch struct {
	Title         *string `json:"title"`
	Product       *string `json:"product"`
	StyleLock     *string `json:"style_lock"`
	Ratio         *string `json:"ratio"`
	VideoProvider *string `json:"video_provider"`
	ImageModel    *string `json:"image_model"`
	Variants      *int    `json:"variants"`
}
type ShotPatch struct {
	Purpose          *string `json:"purpose"`
	DurationS        *int    `json:"duration_s"`
	FirstFramePrompt *string `json:"first_frame_prompt"`
	MotionPrompt     *string `json:"motion_prompt"`
	Negative         *string `json:"negative"`
	Keep             *string `json:"keep"`
	AfterPosition    *int    `json:"after_position"`
}
type GenerateRequest struct {
	ShotIDs  []string `json:"shot_ids"`
	Variants int      `json:"variants"`
	Provider string   `json:"provider"`
}
type Progress struct {
	TakesTotal   int `json:"takes_total"`
	TakesDone    int `json:"takes_done"`
	TakesFailed  int `json:"takes_failed"`
	TakesRunning int `json:"takes_running"`
}
type Project struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	ReferenceID    string    `json:"reference_id"`
	AnalysisID     string    `json:"analysis_id"`
	Product        string    `json:"product"`
	StyleLock      string    `json:"style_lock"`
	Ratio          string    `json:"ratio"`
	VideoProvider  string    `json:"video_provider"`
	ImageModel     string    `json:"image_model"`
	Variants       int       `json:"variants"`
	Status         string    `json:"status"`
	FinalURL       string    `json:"final_url"`
	FinalDurationS float64   `json:"final_duration_s"`
	QCID           string    `json:"qc_id"`
	Error          string    `json:"error"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Shots          []Shot    `json:"shots"`
	Progress       Progress  `json:"progress"`
}
type Shot struct {
	ID               string  `json:"id"`
	Position         int     `json:"position"`
	Purpose          string  `json:"purpose"`
	SourceStartS     float64 `json:"source_start_s"`
	SourceEndS       float64 `json:"source_end_s"`
	DurationS        int     `json:"duration_s"`
	FirstFramePrompt string  `json:"first_frame_prompt"`
	MotionPrompt     string  `json:"motion_prompt"`
	Negative         string  `json:"negative"`
	Keep             string  `json:"keep"`
	FirstFrameURL    string  `json:"first_frame_url"`
	FirstFrameSource string  `json:"first_frame_source"`
	FirstFrameStatus string  `json:"first_frame_status"`
	FirstFrameError  string  `json:"first_frame_error"`
	SelectedTakeID   string  `json:"selected_take_id"`
	Takes            []Take  `json:"takes"`
}
type Take struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	DurationS float64   `json:"duration_s"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	URL       string    `json:"url"`
	CostNote  string    `json:"cost_note"`
	CreatedAt time.Time `json:"created_at"`
}
type Provider struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Note            string `json:"note"`
	Available       bool   `json:"available"`
	Keys            int    `json:"keys,omitempty"`
	Durations       []int  `json:"durations"`
	DefaultDuration int    `json:"default_duration"`
}
type ProvidersResponse struct {
	Providers       []Provider `json:"providers"`
	DefaultProvider string     `json:"default_provider"`
}
type Skipped struct {
	ShotID string `json:"shot_id"`
	Reason string `json:"reason"`
}
type GenerateResponse struct {
	Project Project   `json:"project"`
	Skipped []Skipped `json:"skipped"`
}
