package hint

// Task is the kind of inference a request performs. It is derived from the
// endpoint path and stored with each activity record.
type Task string

const (
	TaskUnknown         Task = ""
	TaskChat            Task = "chat" // chat and text completion endpoints
	TaskEmbedding       Task = "embedding"
	TaskRerank          Task = "rerank"
	TaskTokenCount      Task = "token_count"
	TaskImageGeneration Task = "image_generation"
	TaskImageEdit       Task = "image_edit"
	TaskSpeech          Task = "speech"
	TaskTranscription   Task = "transcription"
	TaskDecision        Task = "decision"
	TaskAudio           Task = "audio"
)
