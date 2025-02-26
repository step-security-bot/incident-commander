package responder

import (
	"database/sql"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"

	"github.com/flanksource/incident-commander/api"
	"github.com/flanksource/incident-commander/db"
)

func getRootHypothesisOfIncident(incidentID uuid.UUID) (api.Hypothesis, error) {
	var hypothesis api.Hypothesis
	if err := db.Gorm.Where("incident_id = ? AND type = ?", incidentID, "root").First(&hypothesis).Error; err != nil {
		return hypothesis, err
	}
	return hypothesis, nil
}

func SyncComments() {
	logger.Debugf("Syncing comments")
	ctx := api.NewContext(db.Gorm, nil)

	var responders []api.Responder
	err := db.Gorm.Where("external_id IS NOT NULL").Preload("Team").Find(&responders).Error
	if err != nil {
		logger.Errorf("Error fetching responders from database: %v", err)
		return
	}

	dbSelectExternalIDQuery := `
        SELECT external_id FROM comments WHERE responder_id = @responder_id
        UNION
        SELECT external_id FROM comment_responders WHERE responder_id = @responder_id
    `

	jobHistory := models.NewJobHistory("ResponderCommentSync", "", "")
	_ = db.PersistJobHistory(ctx, jobHistory.Start())
	for _, responder := range responders {
		if !responder.Team.HasResponder() {
			logger.Debugf("Skipping responder %s since it does not have a responder", responder.Team.Name)
			continue
		}

		responderClient, err := GetResponder(ctx, responder.Team)
		if err != nil {
			logger.Errorf("Error getting responder: %v", err)
			jobHistory.AddError(err.Error())
			continue
		}

		comments, err := responderClient.GetComments(responder.ExternalID)
		if err != nil {
			logger.Errorf("Error fetching comments from responder: %v", err)
			jobHistory.AddError(err.Error())
			continue
		}

		// Query all external_ids from comments and comment_responders table
		var dbExternalIDs []string
		err = db.Gorm.Raw(dbSelectExternalIDQuery, sql.Named("responder_id", responder.ID)).Find(&dbExternalIDs).Error
		if err != nil {
			logger.Errorf("Error querying external_ids from database: %v", err)
			jobHistory.AddError(err.Error())
			continue
		}

		// IDs which are in Jira but not added to database must be added in the comments table
		for _, responderComment := range comments {
			if !collections.Contains(dbExternalIDs, responderComment.ExternalID) {
				rootHypothesis, err := getRootHypothesisOfIncident(responder.IncidentID)
				if err != nil {
					logger.Errorf("Error fetching hypothesis from database: %v", err)
					continue
				}
				responderComment.IncidentID = responder.IncidentID
				responderComment.CreatedBy = *api.SystemUserID
				responderComment.ResponderID = &responder.ID
				responderComment.HypothesisID = &rootHypothesis.ID

				err = db.Gorm.Create(&responderComment).Error
				if err != nil {
					logger.Errorf("Error inserting comment in database: %v", err)
					continue
				}
			}
		}
	}
	jobHistory.IncrSuccess()
	_ = db.PersistJobHistory(ctx, jobHistory.End())
}
