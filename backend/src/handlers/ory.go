package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	client "github.com/ory/kratos-client-go"

	log "github.com/sirupsen/logrus"
)

func (srv *Server) registerOryRoutes() []routeDef {
	return []routeDef{newSystemAdminRoute("DELETE /api/identities/sync", srv.handleDeleteAllKratosIdentities)}
}

func (srv *Server) handleDeleteAllKratosIdentities(w http.ResponseWriter, r *http.Request, log sLog) error {
	if err := srv.deleteAllKratosIdentities(r.Context()); err != nil {
		return newInternalServerServiceError(err, "error communicating with Ory Kratos")
	}
	return writeJsonResponse(w, http.StatusNoContent, "identities deleted successfully")
}

func (srv *Server) deleteAllKratosIdentities(ctx context.Context) error {
	identities, err := srv.handleFindKratosIdentities(ctx)
	if err != nil {
		log.Errorln("unable to fetch all identities from Ory Kratos")
		return err
	}
	for _, user := range identities {
		id := user.GetId()
		resp, err := srv.OryClient.IdentityAPI.DeleteIdentity(ctx, id).Execute()
		if err != nil {
			log.WithFields(log.Fields{"identity": id}).Errorln("unable to delete identity from Ory Kratos")
			continue
		}
		if resp.StatusCode != 204 {
			log.WithFields(log.Fields{"identity": id}).Errorln("unable to delete identity from Ory Kratos")
			continue
		} else {
			continue
		}
	}
	return nil
}

func (srv *Server) deleteIdentityInKratos(ctx context.Context, kratosId *string) error {
	if kratosId == nil || *kratosId == "" {
		return nil
	}
	resp, err := srv.OryClient.IdentityAPI.DeleteIdentity(ctx, *kratosId).Execute()
	if err != nil {
		log.WithField("identity", kratosId).Errorln("unable to delete identity from Ory Kratos")
		return err
	}
	if resp.StatusCode != 204 {
		log.WithField("identity", kratosId).Errorln("unable to delete identity from Ory Kratos")
		return errors.New("unable to delete identity from Ory isntance")
	}
	return nil
}

func (srv *Server) handleFindKratosIdentities(ctx context.Context) ([]client.Identity, error) {
	identities, resp, err := srv.OryClient.IdentityAPI.ListIdentities(ctx).Execute()
	if err != nil {
		log.Error("Error getting identities from kratos integration")
		return nil, err
	}
	if resp.StatusCode != 200 {
		log.Errorf("kratos identites response failed with code %d", resp.StatusCode)
		return nil, errors.New("kratos client failed to send request")
	}
	log.WithFields(log.Fields{"identities": identities}).Info("kratos identities found")
	return identities, nil
}

func (srv *Server) validateUserIDKratos(id string) error {
	_, resp, err := srv.OryClient.IdentityAPI.GetIdentity(context.Background(), id).Execute()
	if err != nil {
		log.WithField("id", id).Error("unable to fetch and validate identity in kratos")
		return err
	}
	if resp.StatusCode != 200 {
		log.WithField("id", id).Error("unable to fetch and validate identity in kratos")
		return errors.New("identity not found in kratos instance")
	}
	return nil
}

var errKratosIdentityOwned = errors.New("username is held by a kratos identity that belongs to a live user")

// findKratosIdentityByUsername returns the identity that holds username as its
// login identifier, or nil when there is none.
func (srv *Server) findKratosIdentityByUsername(ctx context.Context, username string) (*client.Identity, error) {
	found, _, err := srv.OryClient.IdentityAPI.ListIdentities(ctx).CredentialsIdentifier(strings.ToLower(username)).Execute()
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, nil
	}
	return &found[0], nil
}

// quarantineKratosIdentity frees the identity's username without deleting it:
// the identity goes inactive and its username gains an ".orphaned.<id>" suffix,
// so Kratos keeps the record that the account once existed.
func (srv *Server) quarantineKratosIdentity(ctx context.Context, identity *client.Identity) error {
	traits, _ := identity.GetTraits().(map[string]interface{})
	if traits == nil {
		traits = map[string]interface{}{}
	}
	id := identity.GetId()
	username, _ := traits["username"].(string)
	traits["username"] = fmt.Sprintf("%s.orphaned.%.8s", username, id)
	body := client.NewUpdateIdentityBody(identity.GetSchemaId(), "inactive", traits)
	_, resp, err := srv.OryClient.IdentityAPI.UpdateIdentity(ctx, id).UpdateIdentityBody(*body).Execute()
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return errors.New("error quarantining identity")
	}
	log.Warnf("quarantined kratos identity %s that no live user holds", id)
	return nil
}

// resolveKratosConflict runs after Kratos answered 409 for user's username: it
// quarantines the identity holding the name unless a live user owns it.
func (srv *Server) resolveKratosConflict(ctx context.Context, user *models.User) error {
	identity, err := srv.findKratosIdentityByUsername(ctx, user.Username)
	if err != nil {
		return err
	}
	if identity == nil {
		return errors.New("kratos reports the username is taken but no identity holds it")
	}
	owned, err := srv.Db.KratosIDInUse(identity.GetId(), user.ID)
	if err != nil {
		return err
	}
	if owned {
		return errKratosIdentityOwned
	}
	return srv.quarantineKratosIdentity(ctx, identity)
}

func (srv *Server) HandleCreateUserKratos(username, password string) error {
	return srv.createUserKratos(username, password, false)
}

// HandleCreateUserKratosRecovering is HandleCreateUserKratos for callers acting
// for an admin, where a username conflict with an identity no live user holds
// is resolved by quarantining that identity. Startup seeding and the other
// callers keep the plain version, which fails on a conflict.
func (srv *Server) HandleCreateUserKratosRecovering(username, password string) error {
	return srv.createUserKratos(username, password, true)
}

func (srv *Server) createUserKratos(username, password string, recover bool) error {
	user, err := srv.Db.GetUserByUsername(username)
	if err != nil {
		log.Error("user not found immediately after creation, this should not happen")
		return err
	}
	ctx := context.Background()
	traits := user.GetTraits()
	traits["password_reset"] = true
	identity := client.NewCreateIdentityBody("default", traits)
	created, resp, err := srv.OryClient.IdentityAPI.CreateIdentity(ctx).CreateIdentityBody(*identity).Execute()
	if recover && resp != nil && resp.StatusCode == http.StatusConflict {
		if err := srv.resolveKratosConflict(ctx, user); err != nil {
			log.Errorf("Error resolving identity conflict: %v", err)
			return err
		}
		created, resp, err = srv.OryClient.IdentityAPI.CreateIdentity(ctx).CreateIdentityBody(*identity).Execute()
	}
	if err != nil {
		log.Errorf("Error creating identity: %v", err)
		return err
	}
	if resp.StatusCode != http.StatusCreated {
		log.Errorf("Error creating identity: %v", resp.StatusCode)
		return errors.New("error creating identity")
	}
	user.KratosID = models.StringPtr(created.GetId())
	// persist the ID before setting the password: if anything below fails, the
	// stored ID lets a password reset repair the identity instead of trying to
	// create a second one with the same (unique) username identifier
	err = srv.Db.UpdateUser(user)
	if err != nil {
		log.Error("Error updating user")
		return err
	}
	claims := &Claims{
		UserID:        user.ID,
		KratosID:      models.FormatNullableString(user.KratosID),
		PasswordReset: true,
		FacilityID:    user.FacilityID,
	}
	err = srv.handleUpdatePasswordKratos(claims, password, true)
	if err != nil {
		log.Error("Error updating password for new kratos user")
		return err
	}
	log.Infof("user created successfully + identity registered with kratos: %v", user)
	return nil
}

func (srv *Server) updateUserTraitsInKratos(claims *Claims) error {
	update := client.UpdateIdentityBody{
		Traits: claims.getTraits(),
	}
	updated, resp, err := srv.OryClient.IdentityAPI.UpdateIdentity(context.Background(), claims.KratosID).UpdateIdentityBody(update).Execute()
	if err != nil {
		log.Errorf("Error updating identity: %v", err)
		return err
	}
	if resp.StatusCode != http.StatusOK {
		log.Errorf("Error updating identity: %v", resp.StatusCode)
		return errors.New("error updating identity")
	}
	log.Infof("identity updated successfully: %v", updated.GetId())
	return nil
}

func (srv *Server) handleUpdatePasswordKratos(claims *Claims, password string, reset bool) error {
	if claims.KratosID == "" {
		return errors.New("kratos ID is empty, please create user in kratos first")
	}
	log.Tracef("updating password for user: %s and KratosID: %s", claims.Username, claims.KratosID)
	identity, resp, err := srv.OryClient.IdentityAPI.GetIdentity(context.Background(), claims.KratosID).Execute()
	if err != nil {
		log.Errorf("Error fetching identity: %v", err)
		return err
	}
	if resp.StatusCode != http.StatusOK {
		log.Errorf("Error fetching identity: %v", resp.StatusCode)
		return errors.New("error fetching identity")
	}
	traits := identity.GetTraits().(map[string]interface{})
	traits["password_reset"] = reset
	body := map[string]interface{}{
		"credentials": map[string]interface{}{
			"password": map[string]interface{}{
				"config": map[string]interface{}{
					"password": password,
				},
			},
		},
		"metadata_admin":  nil,
		"metadata_public": nil,
		"schema_id":       identity.GetSchemaId(),
		"state":           identity.GetState(),
		"traits":          traits,
	}
	byte, err := json.Marshal(body)
	if err != nil {
		log.Errorf("Error updating identity with user password: %v", err)
		return err
	}
	req, err := http.NewRequest(http.MethodPut, os.Getenv("KRATOS_ADMIN_URL")+"/admin/identities/"+identity.GetId(), bytes.NewBuffer(byte))
	if err != nil {
		log.Errorf("Error creating identity request to kratos: %v", err)
		return err
	}
	req.Header.Set("Authorization", "Bearer "+os.Getenv("ORY_TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	resp, err = srv.Client.Do(req)
	if err != nil {
		log.Errorf("Error sending identity request to kratos: %v", err)
		return err
	}
	if resp.StatusCode != http.StatusOK {
		log.Errorf("Error updating identity with user password: %v", resp.StatusCode)
		return errors.New("error updating identity")
	}
	log.Infof("password updated successfully for user: %s and KratosID: %s", claims.Username, identity.GetId())
	return nil
}

func (srv *Server) updateFacilityInKratosIdentity(userID int, transFacilityID int) error {
	ctx := context.Background()
	user, err := srv.Db.GetUserByID(uint(userID))
	if err != nil {
		log.Errorf("error retrieving user with id %d: %v", userID, err)
		return err
	}
	if user.KratosID == nil {
		log.Errorf("user %d has no Kratos identity ID, skipping identity update", userID)
		return nil
	}
	kratosID := *user.KratosID
	identity, resp, err := srv.OryClient.IdentityAPI.GetIdentity(ctx, kratosID).Execute()
	if err != nil {
		log.Errorf("error fetching identity using id %s: %v", kratosID, err)
		return err
	}
	if resp.StatusCode != http.StatusOK {
		log.Errorf("error fetching identity using id %s; status code: %s", kratosID, resp.Status)
		return errors.New("error fetching identity")
	}
	traits := identity.GetTraits().(map[string]interface{})
	traits["facility_id"] = transFacilityID
	update := client.UpdateIdentityBody{
		Traits: traits,
	}
	updated, resp, err := srv.OryClient.IdentityAPI.UpdateIdentity(ctx, kratosID).UpdateIdentityBody(update).Execute()
	if err != nil {
		log.Errorf("error updating identity with new facility id %d: %v", transFacilityID, err)
		return err
	}
	if resp.StatusCode != http.StatusOK {
		log.Errorf("error updating identity with new facility id %d: %v", transFacilityID, resp.Status)
		return errors.New("failed to update identity")
	}
	log.Infof("Identity updated successfully: %v", updated.GetId())
	return nil
}
