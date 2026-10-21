package nextcloud

// Every registered tool needs the target kind its call checks before any request: the kind it names here, or
// folder or talk in addition for talk.files.share. A tool of the calendar, address book, Deck, and Talk
// groups that creates the object it works on, and so cannot be served by one bound object, needs the kind
// alone. The check at the call stays as it is; this table only mirrors it so a binding does not offer what
// the call would refuse.
var (
	folderReachTools = []string{
		commentsCreate.ID, commentsDelete.ID, commentsList.ID, commentsUpdate.ID,
		favoritesList.ID, filesCopy.ID, filesCreate.ID, filesDelete.ID,
		filesFavorite.ID, filesGet.ID, filesList.ID, filesMove.ID,
		filesSearch.ID, filesStat.ID, filesTagsAdd.ID, filesTagsList.ID,
		filesTagsRemove.ID, filesUpdate.ID, foldersCreate.ID, foldersDelete.ID,
		sharesCreate.ID, sharesDelete.ID, sharesGet.ID, sharesList.ID,
		sharesUpdate.ID, trashDelete.ID, trashList.ID, trashRestore.ID,
		versionsGet.ID, versionsList.ID, versionsRestore.ID,
	}
	accountReachTools = []string{
		accountGet.ID, activityList.ID, incomingAccept.ID, incomingDecline.ID,
		incomingPending.ID, notificationsDismiss.ID, notificationsDismissAll.ID, notificationsList.ID,
		remoteSharesGet.ID, remoteSharesList.ID, remoteSharesRemove.ID, searchProvidersTool.ID,
		searchQueryTool.ID, shareesSearch.ID, statusClear.ID, statusGet.ID,
		statusSet.ID, systemtagsCreate.ID, systemtagsDelete.ID, systemtagsList.ID,
		systemtagsUpdate.ID,
	}
	adminReachTools = []string{
		adminGroupsList.ID, adminGroupsMembers.ID, adminUsersGet.ID, adminUsersList.ID,
	}
	deckReachTools = []string{
		deckACLAdd.ID, deckACLRemove.ID, deckACLUpdate.ID, deckAttachmentsDelete.ID,
		deckAttachmentsGet.ID, deckAttachmentsList.ID, deckAttachmentsUpload.ID, deckBoardsCreate.ID,
		deckBoardsDelete.ID, deckBoardsGet.ID, deckBoardsList.ID, deckBoardsUpdate.ID,
		deckCardsAssignees.ID, deckCardsCreate.ID, deckCardsDelete.ID, deckCardsGet.ID,
		deckCardsLabels.ID, deckCardsMove.ID, deckCardsUpdate.ID, deckCommentsCreate.ID,
		deckCommentsDelete.ID, deckCommentsList.ID, deckCommentsUpdate.ID, deckLabelsCreate.ID,
		deckLabelsDelete.ID, deckLabelsUpdate.ID, deckStacksCreate.ID, deckStacksDelete.ID,
		deckStacksList.ID, deckStacksUpdate.ID,
	}
	talkReachTools = []string{
		talkFilesShare.ID, talkMessagesDelete.ID, talkMessagesEdit.ID, talkMessagesList.ID,
		talkMessagesSend.ID, talkParticipantsAdd.ID, talkParticipantsList.ID, talkParticipantsModerator.ID,
		talkParticipantsRemove.ID, talkPollsClose.ID, talkPollsCreate.ID, talkPollsGet.ID,
		talkPollsVote.ID, talkReactionsSet.ID, talkRoomsCreate.ID, talkRoomsDelete.ID,
		talkRoomsGet.ID, talkRoomsList.ID, talkRoomsMarkRead.ID, talkRoomsUpdate.ID,
	}
	notesReachTools = []string{
		noteAttachmentsUpload.ID, notesAttachmentsGet.ID, notesCreate.ID, notesDelete.ID,
		notesGet.ID, notesList.ID, notesSettingsGet.ID, notesUpdate.ID,
	}
	calendarReachTools = []string{
		calendarsCreate.ID, calendarsDelete.ID, calendarsList.ID, calendarsShare.ID,
		calendarsUnshare.ID, calendarsUpdate.ID, calendarTrashDelete.ID, calendarTrashList.ID,
		calendarTrashRestore.ID, eventsCreate.ID, eventsDelete.ID, eventsGet.ID,
		eventsList.ID, eventsUpdate.ID, tasksCreate.ID, tasksDelete.ID,
		tasksGet.ID, tasksList.ID, tasksUpdate.ID,
	}
	addressbookReachTools = []string{
		addressbooksCreate.ID, addressbooksDelete.ID, addressbooksList.ID, addressbooksShare.ID,
		addressbooksUnshare.ID, addressbooksUpdate.ID, contactsCreate.ID, contactsDelete.ID,
		contactsGet.ID, contactsList.ID, contactsUpdate.ID,
	}

	// createsWholeKind lists the tools that need the target of a kind alone: one bound object could not hold
	// the object they create.
	createsWholeKind = map[string]string{
		calendarsCreate.ID:    kindCalendar,
		addressbooksCreate.ID: kindAddressbook,
		deckBoardsCreate.ID:   kindDeck,
		talkRoomsCreate.ID:    kindTalk,
	}
)

// toolKinds maps a tool ID to the kinds its call needs, built once from the lists above.
var toolKinds = func() map[string][]string {
	m := map[string][]string{}
	for kind, ids := range map[string][]string{
		kindFolder: folderReachTools, kindAccount: accountReachTools, kindAdmin: adminReachTools, kindDeck: deckReachTools,
		kindTalk: talkReachTools, kindNotes: notesReachTools, kindCalendar: calendarReachTools, kindAddressbook: addressbookReachTools,
	} {
		for _, id := range ids {
			m[id] = append(m[id], kind)
		}
	}
	m[talkFilesShare.ID] = []string{kindFolder, kindTalk}
	return m
}()

// kindForms are the forms of a kind a binding can take to reach a tool: the kind alone, and for a kind that
// takes an ID also one object.
func kindForms(kind string) []string {
	for _, k := range targetKinds {
		if k.Name == kind {
			return k.Forms
		}
	}
	return nil
}

// reaches says whether a binding reaches a tool, mirroring what the call itself refuses. A binding that
// does not parse reaches everything; configuration validation reports it. The first missing kind is named.
func reaches(tool string, targets []string) (bool, []string) {
	bound, err := parseScope(targets)
	if err != nil {
		return true, nil
	}
	for _, kind := range toolKinds[tool] {
		have, whole := false, false
		switch kind {
		case kindFolder:
			have, whole = bound.hasFolder, bound.hasFolder
		case kindAccount:
			have, whole = bound.account, bound.account
		case kindAdmin:
			have, whole = bound.admin, bound.admin
		default:
			sel := map[string]selection{kindCalendar: bound.calendars, kindAddressbook: bound.addressbooks,
				kindTalk: bound.talks, kindDeck: bound.decks, kindNotes: bound.notes}[kind]
			have, whole = sel.bound(), sel.all
		}
		if wanted, ok := createsWholeKind[tool]; ok && wanted == kind {
			if !whole {
				return false, []string{kind}
			}
		} else if !have {
			return false, kindForms(kind)
		}
	}
	return true, nil
}
