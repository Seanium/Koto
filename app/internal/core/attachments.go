package core

// AttachmentQuestions resolves only explicit native parent links. A file snapshot
// recorded after a question is part of that question, not another human turn or
// an assistant reply. An orphan attachment remains independently selectable.
func AttachmentQuestions(path []*Event) map[EventID]EventID {
	questions := map[EventID]EventID{}
	owners := map[EventID]EventID{}
	for _, event := range path {
		if event.Kind == "message" && event.Actor == "user" {
			questions[event.ID] = event.ID
			continue
		}
		if event.Kind != "attachment" || event.Actor != "user" || event.Attrs["sidechain"] == true || len(event.Parents) != 1 {
			continue
		}
		if question := questions[event.Parents[0]]; question != "" {
			questions[event.ID] = question
			owners[event.ID] = question
		}
	}
	return owners
}
