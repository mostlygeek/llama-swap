package version

type VersionStruct struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

var (
	Version     string = "0"
	Commit      string = "abcd1234"
	Date        string = "unknown"
	VersionJSON        = VersionStruct{
		Version: Version,
		Commit:  Commit,
		Date:    Date,
	}
)
