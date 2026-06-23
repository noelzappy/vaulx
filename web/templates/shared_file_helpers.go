package templates

import "strings"

func sharedMimeLabel(mimeType string) string {
	switch {
	case len(mimeType) >= 5 && mimeType[:5] == "image":
		return "Image"
	case len(mimeType) >= 5 && mimeType[:5] == "video":
		return "Video"
	case len(mimeType) >= 5 && mimeType[:5] == "audio":
		return "Audio"
	case mimeType == "application/pdf":
		return "PDF"
	case mimeType == "application/zip":
		return "ZIP"
	case mimeType != "":
		return mimeType
	default:
		return "File"
	}
}

func sharedFileExt(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx < 0 || idx == len(name)-1 {
		return ""
	}
	return strings.ToUpper(name[idx+1:])
}
