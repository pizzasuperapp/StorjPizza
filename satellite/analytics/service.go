// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package analytics

import (
	"context"
	"strings"

	"github.com/zeebo/errs"
	"go.uber.org/zap"
	segment "gopkg.in/segmentio/analytics-go.v3"

	"storj.io/common/uuid"
)

const (
	eventAccountCreated             = "Account Created"
	eventSignedIn                   = "Signed In"
	eventProjectCreated             = "Project Created"
	eventAccessGrantCreated         = "Access Grant Created"
	eventAccountVerified            = "Account Verified"
	eventGatewayCredentialsCreated  = "Credentials Created"
	eventPassphraseCreated          = "Passphrase Created"
	eventExternalLinkClicked        = "External Link Clicked"
	eventPathSelected               = "Path Selected"
	eventLinkShared                 = "Link Shared"
	eventObjectUploaded             = "Object Uploaded"
	eventAPIKeyGenerated            = "API Key Generated"
	eventCreditCardAdded            = "Credit Card Added"
	eventUpgradeBannerClicked       = "Upgrade Banner Clicked"
	eventModalAddCard               = "Credit Card Added In Modal"
	eventModalAddTokens             = "Storj Token Added In Modal"
	eventSearchBuckets              = "Search Buckets"
	eventNavigateProjects           = "Navigate Projects"
	eventManageProjectsClicked      = "Manage Projects Clicked"
	eventCreateNewClicked           = "Create New Clicked"
	eventViewDocsClicked            = "View Docs Clicked"
	eventViewForumClicked           = "View Forum Clicked"
	eventViewSupportClicked         = "View Support Clicked"
	eventCreateAnAccessGrantClicked = "Create an Access Grant Clicked"
	eventUploadUsingCliClicked      = "Upload Using CLI Clicked"
	eventUploadInWebClicked         = "Upload In Web Clicked"
	eventNewProjectClicked          = "New Project Clicked"
	eventLogoutClicked              = "Logout Clicked"
	eventProfileUpdated             = "Profile Updated"
	eventPasswordChanged            = "Password Changed"
	eventMfaEnabled                 = "MFA Enabled"
	eventBucketCreated              = "Bucket Created"
	eventBucketDeleted              = "Bucket Deleted"
	eventProjectLimitError          = "Project Limit Error"
)

var (
	// Error is the default error class the analytics package.
	Error = errs.Class("analytics service")
)

// Config is a configuration struct for analytics Service.
type Config struct {
	SegmentWriteKey string `help:"segment write key" default:""`
	Enabled         bool   `help:"enable analytics reporting" default:"false"`
	HubSpot         HubSpotConfig
}

// Service for sending analytics.
//
// architecture: Service
type Service struct {
	log           *zap.Logger
	config        Config
	satelliteName string
	clientEvents  map[string]bool

	segment segment.Client
	hubspot *HubSpotEvents
}

// NewService creates new service for creating sending analytics.
func NewService(log *zap.Logger, config Config, satelliteName string) *Service {
	service := &Service{
		log:           log,
		config:        config,
		satelliteName: satelliteName,
		clientEvents:  make(map[string]bool),
		hubspot:       NewHubSpotEvents(log.Named("hubspotclient"), config.HubSpot, satelliteName),
	}
	if config.Enabled {
		service.segment = segment.New(config.SegmentWriteKey)
	}
	for _, name := range []string{eventGatewayCredentialsCreated, eventPassphraseCreated, eventExternalLinkClicked,
		eventPathSelected, eventLinkShared, eventObjectUploaded, eventAPIKeyGenerated, eventUpgradeBannerClicked,
		eventModalAddCard, eventModalAddTokens, eventSearchBuckets, eventNavigateProjects, eventManageProjectsClicked,
		eventCreateNewClicked, eventViewDocsClicked, eventViewForumClicked, eventViewSupportClicked, eventCreateAnAccessGrantClicked,
		eventUploadUsingCliClicked, eventUploadInWebClicked, eventNewProjectClicked, eventLogoutClicked, eventProfileUpdated,
		eventPasswordChanged, eventMfaEnabled, eventBucketCreated, eventBucketDeleted} {
		service.clientEvents[name] = true
	}

	return service
}

// Run runs the service and use the context in new requests.
func (service *Service) Run(ctx context.Context) error {
	if !service.config.Enabled {
		return nil
	}
	return service.hubspot.Run(ctx)
}

// Close closes the Segment client.
func (service *Service) Close() error {
	if !service.config.Enabled {
		return nil
	}
	return service.segment.Close()
}

// UserType is a type for distinguishing personal vs. professional users.
type UserType string

const (
	// Professional defines a "professional" user type.
	Professional UserType = "Professional"
	// Personal defines a "personal" user type.
	Personal UserType = "Personal"
)

// TrackCreateUserFields contains input data for tracking a create user event.
type TrackCreateUserFields struct {
	ID               uuid.UUID
	AnonymousID      string
	FullName         string
	Email            string
	Type             UserType
	EmployeeCount    string
	CompanyName      string
	JobTitle         string
	HaveSalesContact bool
	OriginHeader     string
	Referrer         string
	HubspotUTK       string
}

func (service *Service) enqueueMessage(message segment.Message) {
	err := service.segment.Enqueue(message)
	if err != nil {
		service.log.Error("Error enqueueing message", zap.Error(err))
	}
}

// TrackCreateUser sends an "Account Created" event to Segment.
func (service *Service) TrackCreateUser(fields TrackCreateUserFields) {
	if !service.config.Enabled {
		return
	}

	fullName := fields.FullName
	names := strings.SplitN(fullName, " ", 2)

	var firstName string
	var lastName string

	if len(names) > 1 {
		firstName = names[0]
		lastName = names[1]
	} else {
		firstName = fullName
	}

	traits := segment.NewTraits()
	traits.SetFirstName(firstName)
	traits.SetLastName(lastName)
	traits.SetEmail(fields.Email)
	traits.Set("lifecyclestage", "customer")
	traits.Set("origin_header", fields.OriginHeader)
	traits.Set("signup_referrer", fields.Referrer)
	traits.Set("account_created", true)

	service.enqueueMessage(segment.Identify{
		UserId:      fields.ID.String(),
		AnonymousId: fields.AnonymousID,
		Traits:      traits,
	})

	props := segment.NewProperties()
	props.Set("email", fields.Email)
	props.Set("name", fields.FullName)
	props.Set("satellite_selected", service.satelliteName)
	props.Set("account_type", fields.Type)
	props.Set("origin_header", fields.OriginHeader)
	props.Set("signup_referrer", fields.Referrer)
	props.Set("account_created", true)

	if fields.Type == Professional {
		props.Set("company_size", fields.EmployeeCount)
		props.Set("company_name", fields.CompanyName)
		props.Set("job_title", fields.JobTitle)
		props.Set("have_sales_contact", fields.HaveSalesContact)
	}

	service.enqueueMessage(segment.Track{
		UserId:      fields.ID.String(),
		AnonymousId: fields.AnonymousID,
		Event:       service.satelliteName + " " + eventAccountCreated,
		Properties:  props,
	})

	service.hubspot.EnqueueCreateUser(fields)
}

// TrackSignedIn sends an "Signed In" event to Segment.
func (service *Service) TrackSignedIn(userID uuid.UUID, email string) {
	if !service.config.Enabled {
		return
	}

	traits := segment.NewTraits()
	traits.SetEmail(email)

	service.enqueueMessage(segment.Identify{
		UserId: userID.String(),
		Traits: traits,
	})

	props := segment.NewProperties()
	props.Set("email", email)

	service.enqueueMessage(segment.Track{
		UserId:     userID.String(),
		Event:      service.satelliteName + " " + eventSignedIn,
		Properties: props,
	})

	service.hubspot.EnqueueEvent(email, service.satelliteName+"_"+eventSignedIn, map[string]interface{}{
		"userid": userID.String(),
	})
}

// TrackProjectCreated sends an "Project Created" event to Segment.
func (service *Service) TrackProjectCreated(userID uuid.UUID, email string, projectID uuid.UUID, currentProjectCount int) {
	if !service.config.Enabled {
		return
	}

	props := segment.NewProperties()
	props.Set("project_count", currentProjectCount)
	props.Set("project_id", projectID.String())
	props.Set("email", email)

	service.enqueueMessage(segment.Track{
		UserId:     userID.String(),
		Event:      service.satelliteName + " " + eventProjectCreated,
		Properties: props,
	})

	service.hubspot.EnqueueEvent(email, service.satelliteName+"_"+eventProjectCreated, map[string]interface{}{
		"userid":        userID.String(),
		"project_count": currentProjectCount,
		"project_id":    projectID.String(),
	})
}

// TrackAccessGrantCreated sends an "Access Grant Created" event to Segment.
func (service *Service) TrackAccessGrantCreated(userID uuid.UUID, email string) {
	if !service.config.Enabled {
		return
	}

	props := segment.NewProperties()
	props.Set("email", email)

	service.enqueueMessage(segment.Track{
		UserId:     userID.String(),
		Event:      service.satelliteName + " " + eventAccessGrantCreated,
		Properties: props,
	})

	service.hubspot.EnqueueEvent(email, service.satelliteName+"_"+eventAccessGrantCreated, map[string]interface{}{
		"userid": userID.String(),
	})
}

// TrackAccountVerified sends an "Account Verified" event to Segment.
func (service *Service) TrackAccountVerified(userID uuid.UUID, email string) {
	if !service.config.Enabled {
		return
	}

	traits := segment.NewTraits()
	traits.SetEmail(email)

	service.enqueueMessage(segment.Identify{
		UserId: userID.String(),
		Traits: traits,
	})

	props := segment.NewProperties()
	props.Set("email", email)

	service.enqueueMessage(segment.Track{
		UserId:     userID.String(),
		Event:      service.satelliteName + " " + eventAccountVerified,
		Properties: props,
	})

	service.hubspot.EnqueueEvent(email, service.satelliteName+"_"+eventAccountVerified, map[string]interface{}{
		"userid": userID.String(),
	})
}

// TrackEvent sends an arbitrary event associated with user ID to Segment.
// It is used for tracking occurrences of client-side events.
func (service *Service) TrackEvent(eventName string, userID uuid.UUID, email string) {
	if !service.config.Enabled {
		return
	}

	// do not track if the event name is an invalid client-side event
	if !service.clientEvents[eventName] {
		service.log.Error("Invalid client-triggered event", zap.String("eventName", eventName))
		return
	}

	props := segment.NewProperties()
	props.Set("email", email)

	service.enqueueMessage(segment.Track{
		UserId:     userID.String(),
		Event:      service.satelliteName + " " + eventName,
		Properties: props,
	})

	service.hubspot.EnqueueEvent(email, service.satelliteName+"_"+eventName, map[string]interface{}{
		"userid": userID.String(),
	})
}

// TrackLinkEvent sends an arbitrary event and link associated with user ID to Segment.
// It is used for tracking occurrences of client-side events.
func (service *Service) TrackLinkEvent(eventName string, userID uuid.UUID, email, link string) {
	if !service.config.Enabled {
		return
	}

	// do not track if the event name is an invalid client-side event
	if !service.clientEvents[eventName] {
		service.log.Error("Invalid client-triggered event", zap.String("eventName", eventName))
		return
	}

	props := segment.NewProperties()
	props.Set("link", link)
	props.Set("email", email)

	service.enqueueMessage(segment.Track{
		UserId:     userID.String(),
		Event:      service.satelliteName + " " + eventName,
		Properties: props,
	})

	service.hubspot.EnqueueEvent(email, service.satelliteName+"_"+eventName, map[string]interface{}{
		"userid": userID.String(),
		"link":   link,
	})
}

// TrackCreditCardAdded sends an "Credit Card Added" event to Segment.
func (service *Service) TrackCreditCardAdded(userID uuid.UUID, email string) {
	if !service.config.Enabled {
		return
	}

	props := segment.NewProperties()
	props.Set("email", email)

	service.enqueueMessage(segment.Track{
		UserId:     userID.String(),
		Event:      service.satelliteName + " " + eventCreditCardAdded,
		Properties: props,
	})

}

// PageVisitEvent sends a page visit event associated with user ID to Segment.
// It is used for tracking occurrences of client-side events.
func (service *Service) PageVisitEvent(pageName string, userID uuid.UUID, email string) {
	if !service.config.Enabled {
		return
	}

	props := segment.NewProperties()
	props.Set("email", email)
	props.Set("path", pageName)
	props.Set("user_id", userID.String())
	props.Set("satellite", service.satelliteName)

	service.enqueueMessage(segment.Page{
		UserId:     userID.String(),
		Name:       "Page Requested",
		Properties: props,
	})

}

// TrackProjectLimitError sends an "Project Limit Error" event to Segment.
func (service *Service) TrackProjectLimitError(userID uuid.UUID, email string) {
	if !service.config.Enabled {
		return
	}

	props := segment.NewProperties()
	props.Set("email", email)

	service.enqueueMessage(segment.Track{
		UserId:     userID.String(),
		Event:      service.satelliteName + " " + eventProjectLimitError,
		Properties: props,
	})

}
