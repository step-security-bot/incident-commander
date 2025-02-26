package events

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/incident-commander/api"
	"github.com/flanksource/incident-commander/responder"
	pkgResponder "github.com/flanksource/incident-commander/responder"
)

func NewResponderConsumer(db *gorm.DB) EventConsumer {
	return EventConsumer{
		WatchEvents: []string{
			EventIncidentResponderAdded,
			EventIncidentCommentAdded,
		},
		ProcessBatchFunc: processResponderEvents,
		BatchSize:        1,
		Consumers:        1,
		DB:               db,
	}
}

func processResponderEvents(ctx *api.Context, events []api.Event) []*api.Event {
	var failedEvents []*api.Event
	for _, e := range events {
		if err := handleResponderEvent(ctx, e); err != nil {
			e.Error = err.Error()
			failedEvents = append(failedEvents, &e)
		}
	}
	return failedEvents
}

func handleResponderEvent(ctx *api.Context, event api.Event) error {
	switch event.Name {
	case EventIncidentResponderAdded:
		return reconcileResponderEvent(ctx, event)
	case EventIncidentCommentAdded:
		return reconcileCommentEvent(ctx, event)
	default:
		return fmt.Errorf("Unrecognized event name: %s", event.Name)
	}
}

func reconcileResponderEvent(ctx *api.Context, event api.Event) error {
	responderID := event.Properties["id"]

	var responder api.Responder
	err := ctx.DB().Where("id = ? AND external_id is NULL", responderID).Preload("Incident").Preload("Team").Find(&responder).Error
	if err != nil {
		return err
	}

	responderClient, err := pkgResponder.GetResponder(ctx, responder.Team)
	if err != nil {
		return err
	}

	externalID, err := responderClient.NotifyResponder(ctx, responder)
	if err != nil {
		return err
	}
	if externalID != "" {
		// Update external id in responder table
		return ctx.DB().Model(&api.Responder{}).Where("id = ?", responder.ID).Update("external_id", externalID).Error
	}

	if err := addNotificationEvent(ctx, event); err != nil {
		logger.Errorf("failed to add notification publish event for responder: %v", err)
	}

	return nil
}

func reconcileCommentEvent(ctx *api.Context, event api.Event) error {
	commentID := event.Properties["id"]

	var comment api.Comment
	err := ctx.DB().Where("id = ? AND external_id IS NULL", commentID).First(&comment).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			logger.Debugf("Skipping comment %s since it was added via responder", commentID)
			return nil
		}

		return err
	}

	// Get all responders related to a comment
	var responders []api.Responder
	commentRespondersQuery := `
        SELECT * FROM responders WHERE incident_id IN (
            SELECT incident_id FROM comments WHERE id = ?
        )
    `
	if err = ctx.DB().Raw(commentRespondersQuery, commentID).Preload("Team").Find(&responders).Error; err != nil {
		return err
	}

	// For each responder add the comment
	for _, _responder := range responders {
		// Reset externalID to avoid inserting previous iteration's ID
		externalID := ""

		responder, err := responder.GetResponder(ctx, _responder.Team)
		if err != nil {
			return err
		}

		externalID, err = responder.NotifyResponderAddComment(ctx, _responder, comment.Comment)
		if err != nil {
			// TODO: Associate error messages with responderType and handle specific responders when reprocessing
			logger.Errorf("error adding comment to responder:%s %v", _responder.Properties["responderType"], err)
			continue
		}

		// Insert into comment_responders table
		if externalID != "" {
			err = ctx.DB().Exec("INSERT INTO comment_responders (comment_id, responder_id, external_id) VALUES (?, ?, ?)",
				commentID, _responder.ID, externalID).Error
			if err != nil {
				logger.Errorf("error updating comment_responders table: %v", err)
			}
		}
	}

	if err := addNotificationEvent(ctx, event); err != nil {
		logger.Errorf("failed to add notification publish event for comment: %v", err)
	}

	return nil
}
