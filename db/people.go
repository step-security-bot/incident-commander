package db

import (
	"time"

	"github.com/flanksource/duty/models"
	"github.com/flanksource/incident-commander/api"
	"github.com/flanksource/incident-commander/utils"
	"gorm.io/gorm/clause"
)

func UpdateUserProperties(ctx *api.Context, userID string, newProps api.PersonProperties) error {
	var current api.Person
	if err := ctx.DB().Table("people").Where("id = ?", userID).First(&current).Error; err != nil {
		return err
	}

	props, err := utils.MergeStructs(current.Properties, newProps)
	if err != nil {
		return err
	}

	return ctx.DB().Table("people").Where("id = ?", userID).Update("properties", props).Error
}

func UpdateIdentityState(ctx *api.Context, id, state string) error {
	return ctx.DB().Table("identities").Where("id = ?", id).Update("state", state).Error
}

type CreateUserRequest struct {
	Username   string
	Password   string
	Properties models.PersonProperties
}

func CreatePerson(ctx *api.Context, request CreateUserRequest) (string, error) {
	tx := ctx.DB().Begin()
	defer tx.Rollback()

	person := models.Person{
		Name:       request.Username,
		Type:       "agent",
		Properties: request.Properties,
	}

	if err := tx.Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).Create(&person).Error; err != nil {
		return "", err
	}

	accessToken := models.AccessToken{
		Value:     request.Password, // TODO: bcrypt
		PersonID:  person.ID,
		ExpiresAt: time.Now().Add(time.Hour), // TODO: decide on this one
	}
	if err := tx.Create(&accessToken).Error; err != nil {
		return "", err
	}

	return person.ID.String(), tx.Commit().Error
}
