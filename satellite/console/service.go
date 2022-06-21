// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package console

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"net/mail"
	"sort"
	"time"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/spf13/pflag"
	"github.com/stripe/stripe-go/v72"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"storj.io/common/macaroon"
	"storj.io/common/memory"
	"storj.io/common/storj"
	"storj.io/common/uuid"
	"storj.io/private/cfgstruct"
	"storj.io/storj/private/api"
	"storj.io/storj/private/blockchain"
	"storj.io/storj/satellite/accounting"
	"storj.io/storj/satellite/analytics"
	"storj.io/storj/satellite/console/consoleauth"
	"storj.io/storj/satellite/payments"
	"storj.io/storj/satellite/payments/monetary"
	"storj.io/storj/satellite/rewards"
)

var mon = monkit.Package()

const (
	// maxLimit specifies the limit for all paged queries.
	maxLimit = 50

	// TestPasswordCost is the hashing complexity to use for testing.
	TestPasswordCost = bcrypt.MinCost
)

// Error messages.
const (
	unauthorizedErrMsg                   = "You are not authorized to perform this action"
	emailUsedErrMsg                      = "This email is already in use, try another"
	emailNotFoundErrMsg                  = "There are no users with the specified email"
	passwordRecoveryTokenIsExpiredErrMsg = "Your password recovery link has expired, please request another one"
	credentialsErrMsg                    = "Your login credentials are incorrect, please try again"
	lockedAccountErrMsg                  = "Your account is locked, please try again later"
	lockedAccountWithResultErrMsg        = "Your login credentials are incorrect, your account is locked again"
	passwordIncorrectErrMsg              = "Your password needs at least %d characters long"
	projectOwnerDeletionForbiddenErrMsg  = "%s is a project owner and can not be deleted"
	apiKeyWithNameExistsErrMsg           = "An API Key with this name already exists in this project, please use a different name"
	apiKeyWithNameDoesntExistErrMsg      = "An API Key with this name doesn't exist in this project."
	teamMemberDoesNotExistErrMsg         = `There is no account on this Satellite for the user(s) you have entered.
									     Please add team members with active accounts`
	activationTokenExpiredErrMsg = "This activation token has expired, please request another one"
	usedRegTokenErrMsg           = "This registration token has already been used"
	projLimitErrMsg              = "Sorry, project creation is limited for your account. Please contact support!"
)

var (
	// Error describes internal console error.
	Error = errs.Class("console service")

	// ErrUnauthorized is error class for authorization related errors.
	ErrUnauthorized = errs.Class("unauthorized")

	// ErrNoMembership is error type of not belonging to a specific project.
	ErrNoMembership = errs.Class("no membership")

	// ErrTokenExpiration is error type of token reached expiration time.
	ErrTokenExpiration = errs.Class("token expiration")

	// ErrProjLimit is error type of project limit.
	ErrProjLimit = errs.Class("project limit")

	// ErrUsage is error type of project usage.
	ErrUsage = errs.Class("project usage")

	// ErrLoginCredentials occurs when provided invalid login credentials.
	ErrLoginCredentials = errs.Class("login credentials")

	// ErrLoginPassword occurs when provided invalid login password.
	ErrLoginPassword = errs.Class("login password")

	// ErrLockedAccount occurs when user's account is locked.
	ErrLockedAccount = errs.Class("locked")

	// ErrEmailUsed is error type that occurs on repeating auth attempts with email.
	ErrEmailUsed = errs.Class("email used")

	// ErrEmailNotFound occurs when no users have the specified email.
	ErrEmailNotFound = errs.Class("email not found")

	// ErrNoAPIKey is error type that occurs when there is no api key found.
	ErrNoAPIKey = errs.Class("no api key found")

	// ErrRegToken describes registration token errors.
	ErrRegToken = errs.Class("registration token")

	// ErrCaptcha describes captcha validation errors.
	ErrCaptcha = errs.Class("captcha validation")

	// ErrRecoveryToken describes account recovery token errors.
	ErrRecoveryToken = errs.Class("recovery token")
)

// Service is handling accounts related logic.
//
// architecture: Service
type Service struct {
	log, auditLogger  *zap.Logger
	store             DB
	restKeys          RESTKeys
	projectAccounting accounting.ProjectAccounting
	projectUsage      *accounting.Service
	buckets           Buckets
	partners          *rewards.PartnersService
	accounts          payments.Accounts
	depositWallets    payments.DepositWallets
	captchaHandler    CaptchaHandler
	analytics         *analytics.Service
	tokens            *consoleauth.Service

	config Config
}

func init() {
	var c Config
	cfgstruct.Bind(pflag.NewFlagSet("", pflag.PanicOnError), &c, cfgstruct.UseTestDefaults())
	if c.PasswordCost != TestPasswordCost {
		panic("invalid test constant defined in struct tag")
	}
	cfgstruct.Bind(pflag.NewFlagSet("", pflag.PanicOnError), &c, cfgstruct.UseReleaseDefaults())
	if c.PasswordCost != 0 {
		panic("invalid release constant defined in struct tag. should be 0 (=automatic)")
	}
}

// Config keeps track of core console service configuration parameters.
type Config struct {
	PasswordCost                int           `help:"password hashing cost (0=automatic)" testDefault:"4" default:"0"`
	OpenRegistrationEnabled     bool          `help:"enable open registration" default:"false" testDefault:"true"`
	DefaultProjectLimit         int           `help:"default project limits for users" default:"1" testDefault:"5"`
	AsOfSystemTimeDuration      time.Duration `help:"default duration for AS OF SYSTEM TIME" devDefault:"-5m" releaseDefault:"-5m" testDefault:"0"`
	LoginAttemptsWithoutPenalty int           `help:"number of times user can try to login without penalty" default:"3"`
	FailedLoginPenalty          float64       `help:"incremental duration of penalty for failed login attempts in minutes" default:"2.0"`
	SessionDuration             time.Duration `help:"duration a session is valid for" default:"168h"`
	UsageLimits                 UsageLimitsConfig
	Recaptcha                   RecaptchaConfig
	Hcaptcha                    HcaptchaConfig
}

// RecaptchaConfig contains configurations for the reCAPTCHA system.
type RecaptchaConfig struct {
	Enabled   bool   `help:"whether or not reCAPTCHA is enabled for user registration" default:"false"`
	SiteKey   string `help:"reCAPTCHA site key"`
	SecretKey string `help:"reCAPTCHA secret key"`
}

// HcaptchaConfig contains configurations for the hCaptcha system.
type HcaptchaConfig struct {
	Enabled   bool   `help:"whether or not hCaptcha is enabled for user registration" default:"false"`
	SiteKey   string `help:"hCaptcha site key" default:""`
	SecretKey string `help:"hCaptcha secret key"`
}

// Payments separates all payment related functionality.
type Payments struct {
	service *Service
}

// NewService returns new instance of Service.
func NewService(log *zap.Logger, store DB, restKeys RESTKeys, projectAccounting accounting.ProjectAccounting, projectUsage *accounting.Service, buckets Buckets, partners *rewards.PartnersService, accounts payments.Accounts, depositWallets payments.DepositWallets, analytics *analytics.Service, tokens *consoleauth.Service, config Config) (*Service, error) {
	if store == nil {
		return nil, errs.New("store can't be nil")
	}
	if log == nil {
		return nil, errs.New("log can't be nil")
	}
	if config.PasswordCost == 0 {
		config.PasswordCost = bcrypt.DefaultCost
	}

	var captchaHandler CaptchaHandler
	if config.Recaptcha.Enabled {
		captchaHandler = NewDefaultCaptcha(Recaptcha, config.Recaptcha.SecretKey)
	} else if config.Hcaptcha.Enabled {
		captchaHandler = NewDefaultCaptcha(Hcaptcha, config.Hcaptcha.SecretKey)
	}

	return &Service{
		log:               log,
		auditLogger:       log.Named("auditlog"),
		store:             store,
		restKeys:          restKeys,
		projectAccounting: projectAccounting,
		projectUsage:      projectUsage,
		buckets:           buckets,
		partners:          partners,
		accounts:          accounts,
		depositWallets:    depositWallets,
		captchaHandler:    captchaHandler,
		analytics:         analytics,
		tokens:            tokens,
		config:            config,
	}, nil
}

func getRequestingIP(ctx context.Context) (source, forwardedFor string) {
	if req := GetRequest(ctx); req != nil {
		return req.RemoteAddr, req.Header.Get("X-Forwarded-For")
	}
	return "", ""
}

func (s *Service) auditLog(ctx context.Context, operation string, userID *uuid.UUID, email string, extra ...zap.Field) {
	sourceIP, forwardedForIP := getRequestingIP(ctx)
	fields := append(
		make([]zap.Field, 0, len(extra)+5),
		zap.String("operation", operation),
		zap.String("source-ip", sourceIP),
		zap.String("forwarded-for-ip", forwardedForIP),
	)
	if userID != nil {
		fields = append(fields, zap.String("userID", userID.String()))
	}
	if email != "" {
		fields = append(fields, zap.String("email", email))
	}
	fields = append(fields, fields...)
	s.auditLogger.Info("console activity", fields...)
}

func (s *Service) getUserAndAuditLog(ctx context.Context, operation string, extra ...zap.Field) (*User, error) {
	user, err := GetUser(ctx)
	if err != nil {
		sourceIP, forwardedForIP := getRequestingIP(ctx)
		s.auditLogger.Info("console activity unauthorized",
			append(append(
				make([]zap.Field, 0, len(extra)+4),
				zap.String("operation", operation),
				zap.Error(err),
				zap.String("source-ip", sourceIP),
				zap.String("forwarded-for-ip", forwardedForIP),
			), extra...)...)
		return nil, err
	}
	s.auditLog(ctx, operation, &user.ID, user.Email, extra...)
	return user, nil
}

// Payments separates all payment related functionality.
func (s *Service) Payments() Payments {
	return Payments{service: s}
}

// SetupAccount creates payment account for authorized user.
func (payment Payments) SetupAccount(ctx context.Context) (_ payments.CouponType, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "setup payment account")
	if err != nil {
		return payments.NoCoupon, Error.Wrap(err)
	}

	return payment.service.accounts.Setup(ctx, user.ID, user.Email, user.SignupPromoCode)
}

// AccountBalance return account balance.
func (payment Payments) AccountBalance(ctx context.Context) (balance payments.Balance, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "get account balance")
	if err != nil {
		return payments.Balance{}, Error.Wrap(err)
	}

	return payment.service.accounts.Balance(ctx, user.ID)
}

// AddCreditCard is used to save new credit card and attach it to payment account.
func (payment Payments) AddCreditCard(ctx context.Context, creditCardToken string) (err error) {
	defer mon.Task()(&ctx, creditCardToken)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "add credit card")
	if err != nil {
		return Error.Wrap(err)
	}

	err = payment.service.accounts.CreditCards().Add(ctx, user.ID, creditCardToken)
	if err != nil {
		return Error.Wrap(err)
	}

	payment.service.analytics.TrackCreditCardAdded(user.ID, user.Email)

	if !user.PaidTier {
		// put this user into the paid tier and convert projects to upgraded limits.
		err = payment.service.store.Users().UpdatePaidTier(ctx, user.ID, true,
			payment.service.config.UsageLimits.Bandwidth.Paid,
			payment.service.config.UsageLimits.Storage.Paid,
			payment.service.config.UsageLimits.Segment.Paid,
			payment.service.config.UsageLimits.Project.Paid,
		)
		if err != nil {
			return Error.Wrap(err)
		}

		projects, err := payment.service.store.Projects().GetOwn(ctx, user.ID)
		if err != nil {
			return Error.Wrap(err)
		}
		for _, project := range projects {
			if project.StorageLimit == nil || *project.StorageLimit < payment.service.config.UsageLimits.Storage.Paid {
				project.StorageLimit = new(memory.Size)
				*project.StorageLimit = payment.service.config.UsageLimits.Storage.Paid
			}
			if project.BandwidthLimit == nil || *project.BandwidthLimit < payment.service.config.UsageLimits.Bandwidth.Paid {
				project.BandwidthLimit = new(memory.Size)
				*project.BandwidthLimit = payment.service.config.UsageLimits.Bandwidth.Paid
			}
			if project.SegmentLimit == nil || *project.SegmentLimit < payment.service.config.UsageLimits.Segment.Paid {
				*project.SegmentLimit = payment.service.config.UsageLimits.Segment.Paid
			}
			err = payment.service.store.Projects().Update(ctx, &project)
			if err != nil {
				return Error.Wrap(err)
			}
		}
	}

	return nil
}

// MakeCreditCardDefault makes a credit card default payment method.
func (payment Payments) MakeCreditCardDefault(ctx context.Context, cardID string) (err error) {
	defer mon.Task()(&ctx, cardID)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "make credit card default")
	if err != nil {
		return Error.Wrap(err)
	}

	return payment.service.accounts.CreditCards().MakeDefault(ctx, user.ID, cardID)
}

// ProjectsCharges returns how much money current user will be charged for each project which he owns.
func (payment Payments) ProjectsCharges(ctx context.Context, since, before time.Time) (_ []payments.ProjectCharge, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "project charges")
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return payment.service.accounts.ProjectCharges(ctx, user.ID, since, before)
}

// ListCreditCards returns a list of credit cards for a given payment account.
func (payment Payments) ListCreditCards(ctx context.Context) (_ []payments.CreditCard, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "list credit cards")
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return payment.service.accounts.CreditCards().List(ctx, user.ID)
}

// RemoveCreditCard is used to detach a credit card from payment account.
func (payment Payments) RemoveCreditCard(ctx context.Context, cardID string) (err error) {
	defer mon.Task()(&ctx, cardID)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "remove credit card")
	if err != nil {
		return Error.Wrap(err)
	}

	return payment.service.accounts.CreditCards().Remove(ctx, user.ID, cardID)
}

// BillingHistory returns a list of billing history items for payment account.
func (payment Payments) BillingHistory(ctx context.Context) (billingHistory []*BillingHistoryItem, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "get billing history")
	if err != nil {
		return nil, Error.Wrap(err)
	}

	invoices, couponUsages, err := payment.service.accounts.Invoices().ListWithDiscounts(ctx, user.ID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	for _, invoice := range invoices {
		billingHistory = append(billingHistory, &BillingHistoryItem{
			ID:          invoice.ID,
			Description: invoice.Description,
			Amount:      invoice.Amount,
			Status:      invoice.Status,
			Link:        invoice.Link,
			End:         invoice.End,
			Start:       invoice.Start,
			Type:        Invoice,
		})
	}

	txsInfos, err := payment.service.accounts.StorjTokens().ListTransactionInfos(ctx, user.ID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	for _, info := range txsInfos {
		billingHistory = append(billingHistory, &BillingHistoryItem{
			ID:          info.ID.String(),
			Description: "STORJ Token Deposit",
			Amount:      info.AmountCents,
			Received:    info.ReceivedCents,
			Status:      info.Status.String(),
			Link:        info.Link,
			Start:       info.CreatedAt,
			End:         info.ExpiresAt,
			Type:        Transaction,
		})
	}

	charges, err := payment.service.accounts.Charges(ctx, user.ID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	for _, charge := range charges {
		desc := fmt.Sprintf("Payment(%s %s)", charge.CardInfo.Brand, charge.CardInfo.LastFour)

		billingHistory = append(billingHistory, &BillingHistoryItem{
			ID:          charge.ID,
			Description: desc,
			Amount:      charge.Amount,
			Start:       charge.CreatedAt,
			Type:        Charge,
		})
	}

	for _, usage := range couponUsages {
		desc := "Coupon"
		if usage.Coupon.Name != "" {
			desc = usage.Coupon.Name
		}
		if usage.Coupon.PromoCode != "" {
			desc += " (" + usage.Coupon.PromoCode + ")"
		}

		billingHistory = append(billingHistory, &BillingHistoryItem{
			Description: desc,
			Amount:      usage.Amount,
			Start:       usage.PeriodStart,
			End:         usage.PeriodEnd,
			Type:        Coupon,
		})
	}

	bonuses, err := payment.service.accounts.StorjTokens().ListDepositBonuses(ctx, user.ID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	for _, bonus := range bonuses {
		billingHistory = append(billingHistory,
			&BillingHistoryItem{
				Description: fmt.Sprintf("%d%% Bonus for STORJ Token Deposit", bonus.Percentage),
				Amount:      bonus.AmountCents,
				Status:      "Added to balance",
				Start:       bonus.CreatedAt,
				Type:        DepositBonus,
			},
		)
	}

	sort.SliceStable(billingHistory,
		func(i, j int) bool {
			return billingHistory[i].Start.After(billingHistory[j].Start)
		},
	)

	return billingHistory, nil
}

// TokenDeposit creates new deposit transaction for adding STORJ tokens to account balance.
func (payment Payments) TokenDeposit(ctx context.Context, amount int64) (_ *payments.Transaction, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "token deposit")
	if err != nil {
		return nil, Error.Wrap(err)
	}

	tx, err := payment.service.accounts.StorjTokens().Deposit(ctx, user.ID, amount)

	return tx, Error.Wrap(err)
}

// checkOutstandingInvoice returns if the payment account has any unpaid/outstanding invoices or/and invoice items.
func (payment Payments) checkOutstandingInvoice(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "get outstanding invoices")
	if err != nil {
		return err
	}

	invoices, err := payment.service.accounts.Invoices().List(ctx, user.ID)
	if err != nil {
		return err
	}
	if len(invoices) > 0 {
		for _, invoice := range invoices {
			if invoice.Status != string(stripe.InvoiceStatusPaid) {
				return ErrUsage.New("user has unpaid/pending invoices")
			}
		}
	}

	hasItems, err := payment.service.accounts.Invoices().CheckPendingItems(ctx, user.ID)
	if err != nil {
		return err
	}
	if hasItems {
		return ErrUsage.New("user has pending invoice items")
	}
	return nil
}

// checkProjectInvoicingStatus returns error if for the given project there are outstanding project records and/or usage
// which have not been applied/invoiced yet (meaning sent over to stripe).
func (payment Payments) checkProjectInvoicingStatus(ctx context.Context, projectID uuid.UUID) (err error) {
	defer mon.Task()(&ctx)(&err)

	_, err = payment.service.getUserAndAuditLog(ctx, "project invoicing status")
	if err != nil {
		return Error.Wrap(err)
	}

	return payment.service.accounts.CheckProjectInvoicingStatus(ctx, projectID)
}

// checkProjectUsageStatus returns error if for the given project there is some usage for current or previous month.
func (payment Payments) checkProjectUsageStatus(ctx context.Context, projectID uuid.UUID) (err error) {
	defer mon.Task()(&ctx)(&err)

	_, err = payment.service.getUserAndAuditLog(ctx, "project usage status")
	if err != nil {
		return Error.Wrap(err)
	}

	return payment.service.accounts.CheckProjectUsageStatus(ctx, projectID)
}

// ApplyCouponCode applies a coupon code to a Stripe customer
// and returns the coupon corresponding to the code.
func (payment Payments) ApplyCouponCode(ctx context.Context, couponCode string) (coupon *payments.Coupon, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "apply coupon code")
	if err != nil {
		return nil, Error.Wrap(err)
	}

	coupon, err = payment.service.accounts.Coupons().ApplyCouponCode(ctx, user.ID, couponCode)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return coupon, nil
}

// GetCoupon returns the coupon applied to the user's account.
func (payment Payments) GetCoupon(ctx context.Context) (coupon *payments.Coupon, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "get coupon")
	if err != nil {
		return nil, Error.Wrap(err)
	}

	coupon, err = payment.service.accounts.Coupons().GetByUserID(ctx, user.ID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return coupon, nil
}

// checkRegistrationSecret returns a RegistrationToken if applicable (nil if not), and an error
// if and only if the registration shouldn't proceed.
func (s *Service) checkRegistrationSecret(ctx context.Context, tokenSecret RegistrationSecret) (*RegistrationToken, error) {
	if s.config.OpenRegistrationEnabled && tokenSecret.IsZero() {
		// in this case we're going to let the registration happen without a token
		return nil, nil
	}

	// in all other cases, require a registration token
	registrationToken, err := s.store.RegistrationTokens().GetBySecret(ctx, tokenSecret)
	if err != nil {
		return nil, ErrUnauthorized.Wrap(err)
	}
	// if a registration token is already associated with an user ID, that means the token is already used
	// we should terminate the account creation process and return an error
	if registrationToken.OwnerID != nil {
		return nil, ErrValidation.New(usedRegTokenErrMsg)
	}

	return registrationToken, nil
}

// CreateUser gets password hash value and creates new inactive User.
func (s *Service) CreateUser(ctx context.Context, user CreateUser, tokenSecret RegistrationSecret) (u *User, err error) {
	defer mon.Task()(&ctx)(&err)

	mon.Counter("create_user_attempt").Inc(1) //mon:locked

	if s.config.Recaptcha.Enabled || s.config.Hcaptcha.Enabled {
		valid, err := s.captchaHandler.Verify(ctx, user.CaptchaResponse, user.IP)
		if err != nil {
			mon.Counter("create_user_captcha_error").Inc(1) //mon:locked
			s.log.Error("captcha authorization failed", zap.Error(err))
			return nil, ErrCaptcha.Wrap(err)
		}
		if !valid {
			mon.Counter("create_user_captcha_unsuccessful").Inc(1) //mon:locked
			return nil, ErrCaptcha.New("captcha validation unsuccessful")
		}
	}

	if err := user.IsValid(); err != nil {
		// NOTE: error is already wrapped with an appropriated class.
		return nil, err
	}

	registrationToken, err := s.checkRegistrationSecret(ctx, tokenSecret)
	if err != nil {
		return nil, ErrRegToken.Wrap(err)
	}

	verified, unverified, err := s.store.Users().GetByEmailWithUnverified(ctx, user.Email)
	if err != nil {
		return nil, Error.Wrap(err)
	}
	if verified != nil {
		mon.Counter("create_user_duplicate_verified").Inc(1) //mon:locked
		return nil, ErrEmailUsed.New(emailUsedErrMsg)
	} else if len(unverified) != 0 {
		mon.Counter("create_user_duplicate_unverified").Inc(1) //mon:locked
		return nil, ErrEmailUsed.New(emailUsedErrMsg)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(user.Password), s.config.PasswordCost)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	// store data
	err = s.store.WithTx(ctx, func(ctx context.Context, tx DBTx) error {
		userID, err := uuid.New()
		if err != nil {
			return Error.Wrap(err)
		}

		newUser := &User{
			ID:               userID,
			Email:            user.Email,
			FullName:         user.FullName,
			ShortName:        user.ShortName,
			PasswordHash:     hash,
			Status:           Inactive,
			IsProfessional:   user.IsProfessional,
			Position:         user.Position,
			CompanyName:      user.CompanyName,
			EmployeeCount:    user.EmployeeCount,
			HaveSalesContact: user.HaveSalesContact,
			SignupPromoCode:  user.SignupPromoCode,
		}

		if user.UserAgent != nil {
			newUser.UserAgent = user.UserAgent
		}

		if registrationToken != nil {
			newUser.ProjectLimit = registrationToken.ProjectLimit
		} else {
			newUser.ProjectLimit = s.config.UsageLimits.Project.Free
		}

		// TODO: move the project limits into the registration token.
		newUser.ProjectStorageLimit = s.config.UsageLimits.Storage.Free.Int64()
		newUser.ProjectBandwidthLimit = s.config.UsageLimits.Bandwidth.Free.Int64()
		newUser.ProjectSegmentLimit = s.config.UsageLimits.Segment.Free

		u, err = tx.Users().Insert(ctx,
			newUser,
		)
		if err != nil {
			return Error.Wrap(err)
		}

		if registrationToken != nil {
			err = tx.RegistrationTokens().UpdateOwner(ctx, registrationToken.Secret, u.ID)
			if err != nil {
				return Error.Wrap(err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, Error.Wrap(err)
	}

	s.auditLog(ctx, "create user", nil, user.Email)
	mon.Counter("create_user_success").Inc(1) //mon:locked

	return u, nil
}

// TestSwapCaptchaHandler replaces the existing handler for captchas with
// the one specified for use in testing.
func (s *Service) TestSwapCaptchaHandler(h CaptchaHandler) {
	s.captchaHandler = h
}

// GenerateActivationToken - is a method for generating activation token.
func (s *Service) GenerateActivationToken(ctx context.Context, id uuid.UUID, email string) (token string, err error) {
	defer mon.Task()(&ctx)(&err)

	return s.tokens.CreateToken(ctx, id, email)
}

// GeneratePasswordRecoveryToken - is a method for generating password recovery token.
func (s *Service) GeneratePasswordRecoveryToken(ctx context.Context, id uuid.UUID) (token string, err error) {
	defer mon.Task()(&ctx)(&err)

	resetPasswordToken, err := s.store.ResetPasswordTokens().GetByOwnerID(ctx, id)
	if err == nil {
		err := s.store.ResetPasswordTokens().Delete(ctx, resetPasswordToken.Secret)
		if err != nil {
			return "", Error.Wrap(err)
		}
	}

	resetPasswordToken, err = s.store.ResetPasswordTokens().Create(ctx, id)
	if err != nil {
		return "", Error.Wrap(err)
	}

	s.auditLog(ctx, "generate password recovery token", &id, "")

	return resetPasswordToken.Secret.String(), nil
}

// GenerateSessionToken creates a new session and returns the string representation of its token.
func (s *Service) GenerateSessionToken(ctx context.Context, userID uuid.UUID, email, ip, userAgent string) (consoleauth.Token, error) {
	sessionID, err := uuid.New()
	if err != nil {
		return consoleauth.Token{}, Error.Wrap(err)
	}

	_, err = s.store.WebappSessions().Create(ctx, sessionID, userID, ip, userAgent, time.Now().Add(s.config.SessionDuration))
	if err != nil {
		return consoleauth.Token{}, err
	}

	token := consoleauth.Token{Payload: sessionID.Bytes()}

	signature, err := s.tokens.SignToken(token)
	if err != nil {
		return consoleauth.Token{}, err
	}
	token.Signature = signature

	s.auditLog(ctx, "login", &userID, email)

	s.analytics.TrackSignedIn(userID, email)

	return token, nil
}

// ActivateAccount - is a method for activating user account after registration.
func (s *Service) ActivateAccount(ctx context.Context, activationToken string) (user *User, err error) {
	defer mon.Task()(&ctx)(&err)

	parsedActivationToken, err := consoleauth.FromBase64URLString(activationToken)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	valid, err := s.tokens.ValidateToken(parsedActivationToken)
	if err != nil {
		return nil, Error.Wrap(err)
	}
	if !valid {
		return nil, Error.New("incorrect signature")
	}

	claims, err := consoleauth.FromJSON(parsedActivationToken.Payload)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	if time.Now().After(claims.Expiration) {
		return nil, ErrTokenExpiration.New(activationTokenExpiredErrMsg)
	}

	_, err = s.store.Users().GetByEmail(ctx, claims.Email)
	if err == nil {
		return nil, ErrEmailUsed.New(emailUsedErrMsg)
	}

	user, err = s.store.Users().Get(ctx, claims.ID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	status := Active
	err = s.store.Users().Update(ctx, user.ID, UpdateUserRequest{
		Status: &status,
	})
	if err != nil {
		return nil, Error.Wrap(err)
	}
	s.auditLog(ctx, "activate account", &user.ID, user.Email)

	s.analytics.TrackAccountVerified(user.ID, user.Email)

	return user, nil
}

// ResetPassword - is a method for resetting user password.
func (s *Service) ResetPassword(ctx context.Context, resetPasswordToken, password string, passcode string, recoveryCode string, t time.Time) (err error) {
	defer mon.Task()(&ctx)(&err)

	secret, err := ResetPasswordSecretFromBase64(resetPasswordToken)
	if err != nil {
		return ErrRecoveryToken.Wrap(err)
	}
	token, err := s.store.ResetPasswordTokens().GetBySecret(ctx, secret)
	if err != nil {
		return ErrRecoveryToken.Wrap(err)
	}

	user, err := s.store.Users().Get(ctx, *token.OwnerID)
	if err != nil {
		return Error.Wrap(err)
	}

	if user.MFAEnabled {
		if recoveryCode != "" {
			found := false
			for _, code := range user.MFARecoveryCodes {
				if code == recoveryCode {
					found = true
					break
				}
			}
			if !found {
				return ErrUnauthorized.Wrap(ErrMFARecoveryCode.New(mfaRecoveryInvalidErrMsg))
			}
		} else if passcode != "" {
			valid, err := ValidateMFAPasscode(passcode, user.MFASecretKey, t)
			if err != nil {
				return ErrValidation.Wrap(ErrMFAPasscode.Wrap(err))
			}
			if !valid {
				return ErrValidation.Wrap(ErrMFAPasscode.New(mfaPasscodeInvalidErrMsg))
			}
		} else {
			return ErrMFAMissing.New(mfaRequiredErrMsg)
		}
	}

	if err := ValidatePassword(password); err != nil {
		return ErrValidation.Wrap(err)
	}

	if s.tokens.IsExpired(t, token.CreatedAt) {
		return ErrRecoveryToken.Wrap(ErrTokenExpiration.New(passwordRecoveryTokenIsExpiredErrMsg))
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.config.PasswordCost)
	if err != nil {
		return Error.Wrap(err)
	}

	updateRequest := UpdateUserRequest{
		PasswordHash: hash,
	}

	if user.FailedLoginCount != 0 {
		resetFailedLoginCount := 0
		resetLoginLockoutExpirationPtr := &time.Time{}
		updateRequest.FailedLoginCount = &resetFailedLoginCount
		updateRequest.LoginLockoutExpiration = &resetLoginLockoutExpirationPtr
	}

	err = s.store.Users().Update(ctx, user.ID, updateRequest)
	if err != nil {
		return Error.Wrap(err)
	}

	s.auditLog(ctx, "password reset", &user.ID, user.Email)

	if err = s.store.ResetPasswordTokens().Delete(ctx, token.Secret); err != nil {
		return Error.Wrap(err)
	}

	return nil
}

// RevokeResetPasswordToken - is a method to revoke reset password token.
func (s *Service) RevokeResetPasswordToken(ctx context.Context, resetPasswordToken string) (err error) {
	defer mon.Task()(&ctx)(&err)

	secret, err := ResetPasswordSecretFromBase64(resetPasswordToken)
	if err != nil {
		return Error.Wrap(err)
	}

	return s.store.ResetPasswordTokens().Delete(ctx, secret)
}

// Token authenticates User by credentials and returns session token.
func (s *Service) Token(ctx context.Context, request AuthUser) (token consoleauth.Token, err error) {
	defer mon.Task()(&ctx)(&err)

	mon.Counter("login_attempt").Inc(1) //mon:locked

	user, unverified, err := s.store.Users().GetByEmailWithUnverified(ctx, request.Email)
	if user == nil {
		if len(unverified) > 0 {
			mon.Counter("login_email_unverified").Inc(1) //mon:locked
		} else {
			mon.Counter("login_email_invalid").Inc(1) //mon:locked
		}
		return consoleauth.Token{}, ErrLoginCredentials.New(credentialsErrMsg)
	}

	now := time.Now()

	if user.LoginLockoutExpiration.After(now) {
		mon.Counter("login_locked_out").Inc(1) //mon:locked
		return consoleauth.Token{}, ErrLockedAccount.New(lockedAccountErrMsg)
	}

	lockoutExpDate := now.Add(time.Duration(math.Pow(s.config.FailedLoginPenalty, float64(user.FailedLoginCount-1))) * time.Minute)
	handleLockAccount := func() error {
		err = s.UpdateUsersFailedLoginState(ctx, user, &lockoutExpDate)
		if err != nil {
			return err
		}

		mon.Counter("login_failed").Inc(1)                                          //mon:locked
		mon.IntVal("login_user_failed_count").Observe(int64(user.FailedLoginCount)) //mon:locked

		if user.FailedLoginCount == s.config.LoginAttemptsWithoutPenalty {
			mon.Counter("login_lockout_initiated").Inc(1) //mon:locked
			return ErrLockedAccount.New(lockedAccountErrMsg)
		}

		if user.FailedLoginCount > s.config.LoginAttemptsWithoutPenalty {
			mon.Counter("login_lockout_reinitiated").Inc(1) //mon:locked
			return ErrLockedAccount.New(lockedAccountWithResultErrMsg)
		}

		return nil
	}

	err = bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(request.Password))
	if err != nil {
		err = handleLockAccount()
		if err != nil {
			return consoleauth.Token{}, err
		}
		mon.Counter("login_invalid_password").Inc(1) //mon:locked
		return consoleauth.Token{}, ErrLoginPassword.New(credentialsErrMsg)
	}

	if user.MFAEnabled {
		if request.MFARecoveryCode != "" && request.MFAPasscode != "" {
			mon.Counter("login_mfa_conflict").Inc(1) //mon:locked
			return consoleauth.Token{}, ErrMFAConflict.New(mfaConflictErrMsg)
		}

		if request.MFARecoveryCode != "" {
			found := false
			codeIndex := -1
			for i, code := range user.MFARecoveryCodes {
				if code == request.MFARecoveryCode {
					found = true
					codeIndex = i
					break
				}
			}
			if !found {
				err = handleLockAccount()
				if err != nil {
					return consoleauth.Token{}, err
				}
				mon.Counter("login_mfa_recovery_failure").Inc(1) //mon:locked
				return consoleauth.Token{}, ErrMFARecoveryCode.New(mfaRecoveryInvalidErrMsg)
			}

			mon.Counter("login_mfa_recovery_success").Inc(1) //mon:locked

			user.MFARecoveryCodes = append(user.MFARecoveryCodes[:codeIndex], user.MFARecoveryCodes[codeIndex+1:]...)

			err = s.store.Users().Update(ctx, user.ID, UpdateUserRequest{
				MFARecoveryCodes: &user.MFARecoveryCodes,
			})
			if err != nil {
				return consoleauth.Token{}, err
			}
		} else if request.MFAPasscode != "" {
			valid, err := ValidateMFAPasscode(request.MFAPasscode, user.MFASecretKey, time.Now())
			if err != nil {
				err = handleLockAccount()
				if err != nil {
					return consoleauth.Token{}, err
				}

				return consoleauth.Token{}, ErrMFAPasscode.Wrap(err)
			}
			if !valid {
				err = handleLockAccount()
				if err != nil {
					return consoleauth.Token{}, err
				}
				mon.Counter("login_mfa_passcode_failure").Inc(1) //mon:locked
				return consoleauth.Token{}, ErrMFAPasscode.New(mfaPasscodeInvalidErrMsg)
			}
			mon.Counter("login_mfa_passcode_success").Inc(1) //mon:locked
		} else {
			mon.Counter("login_mfa_missing").Inc(1) //mon:locked
			return consoleauth.Token{}, ErrMFAMissing.New(mfaRequiredErrMsg)
		}
	}

	if user.FailedLoginCount != 0 {
		user.FailedLoginCount = 0
		loginLockoutExpirationPtr := &time.Time{}
		err = s.store.Users().Update(ctx, user.ID, UpdateUserRequest{
			FailedLoginCount:       &user.FailedLoginCount,
			LoginLockoutExpiration: &loginLockoutExpirationPtr,
		})
		if err != nil {
			return consoleauth.Token{}, err
		}
	}

	token, err = s.GenerateSessionToken(ctx, user.ID, user.Email, request.IP, request.UserAgent)
	if err != nil {
		return consoleauth.Token{}, err
	}

	mon.Counter("login_success").Inc(1) //mon:locked

	return token, nil
}

// UpdateUsersFailedLoginState updates User's failed login state.
func (s *Service) UpdateUsersFailedLoginState(ctx context.Context, user *User, lockoutExpDate *time.Time) error {
	updateRequest := UpdateUserRequest{}
	if user.FailedLoginCount >= s.config.LoginAttemptsWithoutPenalty-1 {
		updateRequest.LoginLockoutExpiration = &lockoutExpDate
	}
	user.FailedLoginCount++

	updateRequest.FailedLoginCount = &user.FailedLoginCount
	return s.store.Users().Update(ctx, user.ID, updateRequest)
}

// GetUser returns User by id.
func (s *Service) GetUser(ctx context.Context, id uuid.UUID) (u *User, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.store.Users().Get(ctx, id)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return user, nil
}

// GenGetUser returns ResponseUser by request context for generated api.
func (s *Service) GenGetUser(ctx context.Context) (*ResponseUser, api.HTTPError) {
	var err error
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get user")
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	respUser := &ResponseUser{
		ID:                   user.ID,
		FullName:             user.FullName,
		ShortName:            user.ShortName,
		Email:                user.Email,
		PartnerID:            user.PartnerID,
		UserAgent:            user.UserAgent,
		ProjectLimit:         user.ProjectLimit,
		IsProfessional:       user.IsProfessional,
		Position:             user.Position,
		CompanyName:          user.CompanyName,
		EmployeeCount:        user.EmployeeCount,
		HaveSalesContact:     user.HaveSalesContact,
		PaidTier:             user.PaidTier,
		MFAEnabled:           user.MFAEnabled,
		MFARecoveryCodeCount: len(user.MFARecoveryCodes),
	}

	return respUser, api.HTTPError{}
}

// GetUserID returns the User ID from the session.
func (s *Service) GetUserID(ctx context.Context) (id uuid.UUID, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get user ID")
	if err != nil {
		return uuid.UUID{}, Error.Wrap(err)
	}
	return user.ID, nil
}

// GetUserByEmailWithUnverified returns Users by email.
func (s *Service) GetUserByEmailWithUnverified(ctx context.Context, email string) (verified *User, unverified []User, err error) {
	defer mon.Task()(&ctx)(&err)

	verified, unverified, err = s.store.Users().GetByEmailWithUnverified(ctx, email)
	if err != nil {
		return verified, unverified, err
	}

	if verified == nil && len(unverified) == 0 {
		err = ErrEmailNotFound.New(emailNotFoundErrMsg)
	}

	return verified, unverified, err
}

// UpdateAccount updates User.
func (s *Service) UpdateAccount(ctx context.Context, fullName string, shortName string) (err error) {
	defer mon.Task()(&ctx)(&err)
	user, err := s.getUserAndAuditLog(ctx, "update account")
	if err != nil {
		return Error.Wrap(err)
	}

	// validate fullName
	err = ValidateFullName(fullName)
	if err != nil {
		return ErrValidation.Wrap(err)
	}

	user.FullName = fullName
	user.ShortName = shortName
	shortNamePtr := &user.ShortName
	err = s.store.Users().Update(ctx, user.ID, UpdateUserRequest{
		FullName:  &user.FullName,
		ShortName: &shortNamePtr,
	})
	if err != nil {
		return Error.Wrap(err)
	}

	return nil
}

// ChangeEmail updates email for a given user.
func (s *Service) ChangeEmail(ctx context.Context, newEmail string) (err error) {
	defer mon.Task()(&ctx)(&err)
	user, err := s.getUserAndAuditLog(ctx, "change email")
	if err != nil {
		return Error.Wrap(err)
	}

	if _, err := mail.ParseAddress(newEmail); err != nil {
		return ErrValidation.Wrap(err)
	}

	verified, unverified, err := s.store.Users().GetByEmailWithUnverified(ctx, newEmail)
	if err != nil {
		return Error.Wrap(err)
	}
	if verified != nil || len(unverified) != 0 {
		return ErrEmailUsed.New(emailUsedErrMsg)
	}

	user.Email = newEmail
	err = s.store.Users().Update(ctx, user.ID, UpdateUserRequest{
		Email: &user.Email,
	})
	if err != nil {
		return Error.Wrap(err)
	}

	return nil
}

// ChangePassword updates password for a given user.
func (s *Service) ChangePassword(ctx context.Context, pass, newPass string) (err error) {
	defer mon.Task()(&ctx)(&err)
	user, err := s.getUserAndAuditLog(ctx, "change password")
	if err != nil {
		return Error.Wrap(err)
	}

	err = bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(pass))
	if err != nil {
		return ErrUnauthorized.New(credentialsErrMsg)
	}

	if err := ValidatePassword(newPass); err != nil {
		return ErrValidation.Wrap(err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPass), s.config.PasswordCost)
	if err != nil {
		return Error.Wrap(err)
	}

	user.PasswordHash = hash
	err = s.store.Users().Update(ctx, user.ID, UpdateUserRequest{
		PasswordHash: hash,
	})
	if err != nil {
		return Error.Wrap(err)
	}

	return nil
}

// DeleteAccount deletes User.
func (s *Service) DeleteAccount(ctx context.Context, password string) (err error) {
	defer mon.Task()(&ctx)(&err)
	user, err := s.getUserAndAuditLog(ctx, "delete account")
	if err != nil {
		return Error.Wrap(err)
	}

	err = bcrypt.CompareHashAndPassword(user.PasswordHash, []byte(password))
	if err != nil {
		return ErrUnauthorized.New(credentialsErrMsg)
	}

	err = s.Payments().checkOutstandingInvoice(ctx)
	if err != nil {
		return Error.Wrap(err)
	}

	err = s.store.Users().Delete(ctx, user.ID)
	if err != nil {
		return Error.Wrap(err)
	}

	return nil
}

// GetProject is a method for querying project by id.
func (s *Service) GetProject(ctx context.Context, projectID uuid.UUID) (p *Project, err error) {
	defer mon.Task()(&ctx)(&err)
	user, err := s.getUserAndAuditLog(ctx, "get project", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, Error.Wrap(err)
	}

	if _, err = s.isProjectMember(ctx, user.ID, projectID); err != nil {
		return nil, Error.Wrap(err)
	}

	p, err = s.store.Projects().Get(ctx, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return
}

// GetUsersProjects is a method for querying all projects.
func (s *Service) GetUsersProjects(ctx context.Context) (ps []Project, err error) {
	defer mon.Task()(&ctx)(&err)
	user, err := s.getUserAndAuditLog(ctx, "get users projects")
	if err != nil {
		return nil, Error.Wrap(err)
	}

	ps, err = s.store.Projects().GetByUserID(ctx, user.ID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return
}

// GenGetUsersProjects is a method for querying all projects for generated api.
func (s *Service) GenGetUsersProjects(ctx context.Context) (ps []Project, httpErr api.HTTPError) {
	var err error
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get users projects")
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	ps, err = s.store.Projects().GetByUserID(ctx, user.ID)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusInternalServerError,
			Err:    Error.Wrap(err),
		}
	}

	return
}

// GetUsersOwnedProjectsPage is a method for querying paged projects.
func (s *Service) GetUsersOwnedProjectsPage(ctx context.Context, cursor ProjectsCursor) (_ ProjectsPage, err error) {
	defer mon.Task()(&ctx)(&err)
	user, err := s.getUserAndAuditLog(ctx, "get user's owned projects page")
	if err != nil {
		return ProjectsPage{}, Error.Wrap(err)
	}

	projects, err := s.store.Projects().ListByOwnerID(ctx, user.ID, cursor)
	if err != nil {
		return ProjectsPage{}, Error.Wrap(err)
	}

	return projects, nil
}

// CreateProject is a method for creating new project.
func (s *Service) CreateProject(ctx context.Context, projectInfo ProjectInfo) (p *Project, err error) {
	defer mon.Task()(&ctx)(&err)
	user, err := s.getUserAndAuditLog(ctx, "create project")
	if err != nil {
		return nil, Error.Wrap(err)
	}

	currentProjectCount, err := s.checkProjectLimit(ctx, user.ID)
	if err != nil {
		s.analytics.TrackProjectLimitError(user.ID, user.Email)
		return nil, ErrProjLimit.Wrap(err)
	}

	newProjectLimits, err := s.getUserProjectLimits(ctx, user.ID)
	if err != nil {
		return nil, ErrProjLimit.Wrap(err)
	}

	var projectID uuid.UUID
	err = s.store.WithTx(ctx, func(ctx context.Context, tx DBTx) error {
		p, err = tx.Projects().Insert(ctx,
			&Project{
				Description:    projectInfo.Description,
				Name:           projectInfo.Name,
				OwnerID:        user.ID,
				PartnerID:      user.PartnerID,
				UserAgent:      user.UserAgent,
				StorageLimit:   &newProjectLimits.StorageLimit,
				BandwidthLimit: &newProjectLimits.BandwidthLimit,
				SegmentLimit:   &newProjectLimits.SegmentLimit,
			},
		)
		if err != nil {
			return Error.Wrap(err)
		}

		_, err = tx.ProjectMembers().Insert(ctx, user.ID, p.ID)
		if err != nil {
			return Error.Wrap(err)
		}

		projectID = p.ID

		return nil
	})

	if err != nil {
		return nil, Error.Wrap(err)
	}

	s.analytics.TrackProjectCreated(user.ID, user.Email, projectID, currentProjectCount+1)

	return p, nil
}

// GenCreateProject is a method for creating new project for generated api.
func (s *Service) GenCreateProject(ctx context.Context, projectInfo ProjectInfo) (p *Project, httpError api.HTTPError) {
	var err error
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "create project")
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	currentProjectCount, err := s.checkProjectLimit(ctx, user.ID)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusInternalServerError,
			Err:    ErrProjLimit.Wrap(err),
		}
	}

	newProjectLimits, err := s.getUserProjectLimits(ctx, user.ID)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusInternalServerError,
			Err:    ErrProjLimit.Wrap(err),
		}
	}

	var projectID uuid.UUID
	err = s.store.WithTx(ctx, func(ctx context.Context, tx DBTx) error {
		p, err = tx.Projects().Insert(ctx,
			&Project{
				Description:    projectInfo.Description,
				Name:           projectInfo.Name,
				OwnerID:        user.ID,
				PartnerID:      user.PartnerID,
				UserAgent:      user.UserAgent,
				StorageLimit:   &newProjectLimits.StorageLimit,
				BandwidthLimit: &newProjectLimits.BandwidthLimit,
				SegmentLimit:   &newProjectLimits.SegmentLimit,
			},
		)
		if err != nil {
			return Error.Wrap(err)
		}

		_, err = tx.ProjectMembers().Insert(ctx, user.ID, p.ID)
		if err != nil {
			return Error.Wrap(err)
		}

		projectID = p.ID

		return nil
	})

	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusInternalServerError,
			Err:    err,
		}
	}

	s.analytics.TrackProjectCreated(user.ID, user.Email, projectID, currentProjectCount+1)

	return p, httpError
}

// DeleteProject is a method for deleting project by id.
func (s *Service) DeleteProject(ctx context.Context, projectID uuid.UUID) (err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "delete project", zap.String("projectID", projectID.String()))
	if err != nil {
		return Error.Wrap(err)
	}

	_, err = s.isProjectOwner(ctx, user.ID, projectID)
	if err != nil {
		return Error.Wrap(err)
	}

	err = s.checkProjectCanBeDeleted(ctx, user, projectID)
	if err != nil {
		return Error.Wrap(err)
	}

	err = s.store.Projects().Delete(ctx, projectID)
	if err != nil {
		return Error.Wrap(err)
	}

	return nil
}

// GenDeleteProject is a method for deleting project by id for generated API.
func (s *Service) GenDeleteProject(ctx context.Context, projectID uuid.UUID) (httpError api.HTTPError) {
	var err error
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "delete project", zap.String("projectID", projectID.String()))
	if err != nil {
		return api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	_, err = s.isProjectOwner(ctx, user.ID, projectID)
	if err != nil {
		return api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	err = s.checkProjectCanBeDeleted(ctx, user, projectID)
	if err != nil {
		return api.HTTPError{
			Status: http.StatusConflict,
			Err:    Error.Wrap(err),
		}
	}

	err = s.store.Projects().Delete(ctx, projectID)
	if err != nil {
		return api.HTTPError{
			Status: http.StatusInternalServerError,
			Err:    Error.Wrap(err),
		}
	}

	return httpError
}

// UpdateProject is a method for updating project name and description by id.
func (s *Service) UpdateProject(ctx context.Context, projectID uuid.UUID, projectInfo ProjectInfo) (p *Project, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "update project name and description", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, Error.Wrap(err)
	}

	err = ValidateNameAndDescription(projectInfo.Name, projectInfo.Description)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	isMember, err := s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}
	project := isMember.project
	project.Name = projectInfo.Name
	project.Description = projectInfo.Description

	if user.PaidTier {
		if project.BandwidthLimit != nil && *project.BandwidthLimit == 0 {
			return nil, Error.New("current bandwidth limit for project is set to 0 (updating disabled)")
		}
		if project.StorageLimit != nil && *project.StorageLimit == 0 {
			return nil, Error.New("current storage limit for project is set to 0 (updating disabled)")
		}
		if projectInfo.StorageLimit <= 0 || projectInfo.BandwidthLimit <= 0 {
			return nil, Error.New("project limits must be greater than 0")
		}

		if projectInfo.StorageLimit > s.config.UsageLimits.Storage.Paid {
			return nil, Error.New("specified storage limit exceeds allowed maximum for current tier")
		}

		if projectInfo.BandwidthLimit > s.config.UsageLimits.Bandwidth.Paid {
			return nil, Error.New("specified bandwidth limit exceeds allowed maximum for current tier")
		}

		storageUsed, err := s.projectUsage.GetProjectStorageTotals(ctx, projectID)
		if err != nil {
			return nil, Error.Wrap(err)
		}
		if projectInfo.StorageLimit.Int64() < storageUsed {
			return nil, Error.New("cannot set storage limit below current usage")
		}

		bandwidthUsed, err := s.projectUsage.GetProjectBandwidthTotals(ctx, projectID)
		if err != nil {
			return nil, Error.Wrap(err)
		}
		if projectInfo.BandwidthLimit.Int64() < bandwidthUsed {
			return nil, Error.New("cannot set bandwidth limit below current usage")
		}

		project.StorageLimit = new(memory.Size)
		*project.StorageLimit = projectInfo.StorageLimit
		project.BandwidthLimit = new(memory.Size)
		*project.BandwidthLimit = projectInfo.BandwidthLimit
	}

	err = s.store.Projects().Update(ctx, project)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return project, nil
}

// GenUpdateProject is a method for updating project name and description by id for generated api.
func (s *Service) GenUpdateProject(ctx context.Context, projectID uuid.UUID, projectInfo ProjectInfo) (p *Project, httpError api.HTTPError) {
	var err error
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "update project name and description", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	err = ValidateNameAndDescription(projectInfo.Name, projectInfo.Description)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusBadRequest,
			Err:    Error.Wrap(err),
		}
	}

	isMember, err := s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}
	project := isMember.project
	project.Name = projectInfo.Name
	project.Description = projectInfo.Description

	if user.PaidTier {
		if project.BandwidthLimit != nil && *project.BandwidthLimit == 0 {
			return nil, api.HTTPError{
				Status: http.StatusInternalServerError,
				Err:    Error.New("current bandwidth limit for project is set to 0 (updating disabled)"),
			}
		}
		if project.StorageLimit != nil && *project.StorageLimit == 0 {
			return nil, api.HTTPError{
				Status: http.StatusInternalServerError,
				Err:    Error.New("current storage limit for project is set to 0 (updating disabled)"),
			}
		}
		if projectInfo.StorageLimit <= 0 || projectInfo.BandwidthLimit <= 0 {
			return nil, api.HTTPError{
				Status: http.StatusBadRequest,
				Err:    Error.New("project limits must be greater than 0"),
			}
		}

		if projectInfo.StorageLimit > s.config.UsageLimits.Storage.Paid {
			return nil, api.HTTPError{
				Status: http.StatusBadRequest,
				Err:    Error.New("specified storage limit exceeds allowed maximum for current tier"),
			}
		}

		if projectInfo.BandwidthLimit > s.config.UsageLimits.Bandwidth.Paid {
			return nil, api.HTTPError{
				Status: http.StatusBadRequest,
				Err:    Error.New("specified bandwidth limit exceeds allowed maximum for current tier"),
			}
		}

		storageUsed, err := s.projectUsage.GetProjectStorageTotals(ctx, projectID)
		if err != nil {
			return nil, api.HTTPError{
				Status: http.StatusInternalServerError,
				Err:    Error.Wrap(err),
			}
		}
		if projectInfo.StorageLimit.Int64() < storageUsed {
			return nil, api.HTTPError{
				Status: http.StatusBadRequest,
				Err:    Error.New("cannot set storage limit below current usage"),
			}
		}

		bandwidthUsed, err := s.projectUsage.GetProjectBandwidthTotals(ctx, projectID)
		if err != nil {
			return nil, api.HTTPError{
				Status: http.StatusInternalServerError,
				Err:    Error.Wrap(err),
			}
		}
		if projectInfo.BandwidthLimit.Int64() < bandwidthUsed {
			return nil, api.HTTPError{
				Status: http.StatusBadRequest,
				Err:    Error.New("cannot set bandwidth limit below current usage"),
			}
		}

		project.StorageLimit = new(memory.Size)
		*project.StorageLimit = projectInfo.StorageLimit
		project.BandwidthLimit = new(memory.Size)
		*project.BandwidthLimit = projectInfo.BandwidthLimit
	}

	err = s.store.Projects().Update(ctx, project)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusInternalServerError,
			Err:    Error.Wrap(err),
		}
	}

	return project, httpError
}

// AddProjectMembers adds users by email to given project.
func (s *Service) AddProjectMembers(ctx context.Context, projectID uuid.UUID, emails []string) (users []*User, err error) {
	defer mon.Task()(&ctx)(&err)
	user, err := s.getUserAndAuditLog(ctx, "add project members", zap.String("projectID", projectID.String()), zap.Strings("emails", emails))
	if err != nil {
		return nil, Error.Wrap(err)
	}

	if _, err = s.isProjectMember(ctx, user.ID, projectID); err != nil {
		return nil, Error.Wrap(err)
	}

	var userErr errs.Group

	// collect user querying errors
	for _, email := range emails {
		user, err := s.store.Users().GetByEmail(ctx, email)
		if err != nil {
			userErr.Add(err)
			continue
		}

		users = append(users, user)
	}

	if err = userErr.Err(); err != nil {
		return nil, ErrValidation.New(teamMemberDoesNotExistErrMsg)
	}

	// add project members in transaction scope
	err = s.store.WithTx(ctx, func(ctx context.Context, tx DBTx) error {
		for _, user := range users {
			if _, err := tx.ProjectMembers().Insert(ctx, user.ID, projectID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return users, nil
}

// DeleteProjectMembers removes users by email from given project.
func (s *Service) DeleteProjectMembers(ctx context.Context, projectID uuid.UUID, emails []string) (err error) {
	defer mon.Task()(&ctx)(&err)
	user, err := s.getUserAndAuditLog(ctx, "delete project members", zap.String("projectID", projectID.String()), zap.Strings("emails", emails))
	if err != nil {
		return Error.Wrap(err)
	}

	if _, err = s.isProjectMember(ctx, user.ID, projectID); err != nil {
		return Error.Wrap(err)
	}

	var userIDs []uuid.UUID
	var userErr errs.Group

	// collect user querying errors
	for _, email := range emails {
		user, err := s.store.Users().GetByEmail(ctx, email)
		if err != nil {
			userErr.Add(err)
			continue
		}

		isOwner, err := s.isProjectOwner(ctx, user.ID, projectID)
		if isOwner {
			return ErrValidation.New(projectOwnerDeletionForbiddenErrMsg, user.Email)
		}
		if err != nil && !ErrUnauthorized.Has(err) {
			return Error.Wrap(err)
		}

		userIDs = append(userIDs, user.ID)
	}

	if err = userErr.Err(); err != nil {
		return ErrValidation.New(teamMemberDoesNotExistErrMsg)
	}

	// delete project members in transaction scope
	err = s.store.WithTx(ctx, func(ctx context.Context, tx DBTx) error {
		for _, uID := range userIDs {
			err = tx.ProjectMembers().Delete(ctx, uID, projectID)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return Error.Wrap(err)
}

// GetProjectMembers returns ProjectMembers for given Project.
func (s *Service) GetProjectMembers(ctx context.Context, projectID uuid.UUID, cursor ProjectMembersCursor) (pmp *ProjectMembersPage, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get project members", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	if cursor.Limit > maxLimit {
		cursor.Limit = maxLimit
	}

	pmp, err = s.store.ProjectMembers().GetPagedByProjectID(ctx, projectID, cursor)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return
}

// CreateAPIKey creates new api key.
func (s *Service) CreateAPIKey(ctx context.Context, projectID uuid.UUID, name string) (_ *APIKeyInfo, _ *macaroon.APIKey, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "create api key", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, nil, Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, nil, Error.Wrap(err)
	}

	_, err = s.store.APIKeys().GetByNameAndProjectID(ctx, name, projectID)
	if err == nil {
		return nil, nil, ErrValidation.New(apiKeyWithNameExistsErrMsg)
	}

	secret, err := macaroon.NewSecret()
	if err != nil {
		return nil, nil, Error.Wrap(err)
	}

	key, err := macaroon.NewAPIKey(secret)
	if err != nil {
		return nil, nil, Error.Wrap(err)
	}

	apikey := APIKeyInfo{
		Name:      name,
		ProjectID: projectID,
		Secret:    secret,
		PartnerID: user.PartnerID,
		UserAgent: user.UserAgent,
	}

	info, err := s.store.APIKeys().Create(ctx, key.Head(), apikey)
	if err != nil {
		return nil, nil, Error.Wrap(err)
	}

	return info, key, nil
}

// GenCreateAPIKey creates new api key for generated api.
func (s *Service) GenCreateAPIKey(ctx context.Context, requestInfo CreateAPIKeyRequest) (*CreateAPIKeyResponse, api.HTTPError) {
	var err error
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "create api key", zap.String("projectID", requestInfo.ProjectID))
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	projectID, err := uuid.FromString(requestInfo.ProjectID)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusBadRequest,
			Err:    Error.Wrap(err),
		}
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	_, err = s.store.APIKeys().GetByNameAndProjectID(ctx, requestInfo.Name, projectID)
	if err == nil {
		return nil, api.HTTPError{
			Status: http.StatusConflict,
			Err:    ErrValidation.New(apiKeyWithNameExistsErrMsg),
		}
	}

	secret, err := macaroon.NewSecret()
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusInternalServerError,
			Err:    Error.Wrap(err),
		}
	}

	key, err := macaroon.NewAPIKey(secret)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusInternalServerError,
			Err:    Error.Wrap(err),
		}
	}

	apikey := APIKeyInfo{
		Name:      requestInfo.Name,
		ProjectID: projectID,
		Secret:    secret,
		PartnerID: user.PartnerID,
		UserAgent: user.UserAgent,
	}

	info, err := s.store.APIKeys().Create(ctx, key.Head(), apikey)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusInternalServerError,
			Err:    Error.Wrap(err),
		}
	}

	return &CreateAPIKeyResponse{
		Key:     key.Serialize(),
		KeyInfo: info,
	}, api.HTTPError{}
}

// GetAPIKeyInfoByName retrieves an api key by its name and project id.
func (s *Service) GetAPIKeyInfoByName(ctx context.Context, projectID uuid.UUID, name string) (_ *APIKeyInfo, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get api key info",
		zap.String("projectID", projectID.String()),
		zap.String("name", name))

	if err != nil {
		return nil, err
	}

	key, err := s.store.APIKeys().GetByNameAndProjectID(ctx, name, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, key.ProjectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return key, nil
}

// GetAPIKeyInfo retrieves api key by id.
func (s *Service) GetAPIKeyInfo(ctx context.Context, id uuid.UUID) (_ *APIKeyInfo, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get api key info", zap.String("apiKeyID", id.String()))
	if err != nil {
		return nil, err
	}

	key, err := s.store.APIKeys().Get(ctx, id)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, key.ProjectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return key, nil
}

// DeleteAPIKeys deletes api key by id.
func (s *Service) DeleteAPIKeys(ctx context.Context, ids []uuid.UUID) (err error) {
	defer mon.Task()(&ctx)(&err)

	idStrings := make([]string, 0, len(ids))
	for _, id := range ids {
		idStrings = append(idStrings, id.String())
	}

	user, err := s.getUserAndAuditLog(ctx, "delete api keys", zap.Strings("apiKeyIDs", idStrings))
	if err != nil {
		return Error.Wrap(err)
	}

	var keysErr errs.Group

	for _, keyID := range ids {
		key, err := s.store.APIKeys().Get(ctx, keyID)
		if err != nil {
			keysErr.Add(err)
			continue
		}

		_, err = s.isProjectMember(ctx, user.ID, key.ProjectID)
		if err != nil {
			keysErr.Add(ErrUnauthorized.Wrap(err))
			continue
		}
	}

	if err = keysErr.Err(); err != nil {
		return Error.Wrap(err)
	}

	err = s.store.WithTx(ctx, func(ctx context.Context, tx DBTx) error {
		for _, keyToDeleteID := range ids {
			err = tx.APIKeys().Delete(ctx, keyToDeleteID)
			if err != nil {
				return err
			}
		}

		return nil
	})
	return Error.Wrap(err)
}

// DeleteAPIKeyByNameAndProjectID deletes api key by name and project ID.
func (s *Service) DeleteAPIKeyByNameAndProjectID(ctx context.Context, name string, projectID uuid.UUID) (err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "delete api key by name and project ID", zap.String("apiKeyName", name), zap.String("projectID", projectID.String()))
	if err != nil {
		return Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return Error.Wrap(err)
	}

	key, err := s.store.APIKeys().GetByNameAndProjectID(ctx, name, projectID)
	if err != nil {
		return ErrNoAPIKey.New(apiKeyWithNameDoesntExistErrMsg)
	}

	err = s.store.APIKeys().Delete(ctx, key.ID)
	if err != nil {
		return Error.Wrap(err)
	}

	return nil
}

// GetAPIKeys returns paged api key list for given Project.
func (s *Service) GetAPIKeys(ctx context.Context, projectID uuid.UUID, cursor APIKeyCursor) (page *APIKeyPage, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get api keys", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	if cursor.Limit > maxLimit {
		cursor.Limit = maxLimit
	}

	page, err = s.store.APIKeys().GetPagedByProjectID(ctx, projectID, cursor)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return
}

// CreateRESTKey creates a satellite rest key.
func (s *Service) CreateRESTKey(ctx context.Context, expiration time.Duration) (apiKey string, expiresAt time.Time, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "create rest key")
	if err != nil {
		return "", time.Time{}, Error.Wrap(err)
	}

	apiKey, expiresAt, err = s.restKeys.Create(ctx, user.ID, expiration)
	if err != nil {
		return "", time.Time{}, Error.Wrap(err)
	}
	return apiKey, expiresAt, nil
}

// RevokeRESTKey revokes a satellite REST key.
func (s *Service) RevokeRESTKey(ctx context.Context, apiKey string) (err error) {
	defer mon.Task()(&ctx)(&err)

	_, err = s.getUserAndAuditLog(ctx, "revoke rest key")
	if err != nil {
		return Error.Wrap(err)
	}

	err = s.restKeys.Revoke(ctx, apiKey)
	if err != nil {
		return Error.Wrap(err)
	}
	return nil
}

// GetProjectUsage retrieves project usage for a given period.
func (s *Service) GetProjectUsage(ctx context.Context, projectID uuid.UUID, since, before time.Time) (_ *accounting.ProjectUsage, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get project usage", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	projectUsage, err := s.projectAccounting.GetProjectTotal(ctx, projectID, since, before)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return projectUsage, nil
}

// GetBucketTotals retrieves paged bucket total usages since project creation.
func (s *Service) GetBucketTotals(ctx context.Context, projectID uuid.UUID, cursor accounting.BucketUsageCursor, before time.Time) (_ *accounting.BucketUsagePage, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get bucket totals", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	usage, err := s.projectAccounting.GetBucketTotals(ctx, projectID, cursor, before)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return usage, nil
}

// GetAllBucketNames retrieves all bucket names of a specific project.
func (s *Service) GetAllBucketNames(ctx context.Context, projectID uuid.UUID) (_ []string, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get all bucket names", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	listOptions := storj.BucketListOptions{
		Direction: storj.Forward,
	}

	allowedBuckets := macaroon.AllowedBuckets{
		All: true,
	}

	bucketsList, err := s.buckets.ListBuckets(ctx, projectID, listOptions, allowedBuckets)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	var list []string
	for _, bucket := range bucketsList.Items {
		list = append(list, bucket.Name)
	}

	return list, nil
}

// GetBucketUsageRollups retrieves summed usage rollups for every bucket of particular project for a given period.
func (s *Service) GetBucketUsageRollups(ctx context.Context, projectID uuid.UUID, since, before time.Time) (_ []accounting.BucketUsageRollup, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get bucket usage rollups", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	result, err := s.projectAccounting.GetBucketUsageRollups(ctx, projectID, since, before)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return result, nil
}

// GenGetBucketUsageRollups retrieves summed usage rollups for every bucket of particular project for a given period for generated api.
func (s *Service) GenGetBucketUsageRollups(ctx context.Context, projectID uuid.UUID, since, before time.Time) (rollups []accounting.BucketUsageRollup, httpError api.HTTPError) {
	var err error
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get bucket usage rollups", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	rollups, err = s.projectAccounting.GetBucketUsageRollups(ctx, projectID, since, before)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusInternalServerError,
			Err:    Error.Wrap(err),
		}
	}

	return
}

// GenGetSingleBucketUsageRollup retrieves usage rollup for single bucket of particular project for a given period for generated api.
func (s *Service) GenGetSingleBucketUsageRollup(ctx context.Context, projectID uuid.UUID, bucket string, since, before time.Time) (rollup *accounting.BucketUsageRollup, httpError api.HTTPError) {
	var err error
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get single bucket usage rollup", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusUnauthorized,
			Err:    Error.Wrap(err),
		}
	}

	rollup, err = s.projectAccounting.GetSingleBucketUsageRollup(ctx, projectID, bucket, since, before)
	if err != nil {
		return nil, api.HTTPError{
			Status: http.StatusInternalServerError,
			Err:    Error.Wrap(err),
		}
	}

	return
}

// GetDailyProjectUsage returns daily usage by project ID.
func (s *Service) GetDailyProjectUsage(ctx context.Context, projectID uuid.UUID, from, to time.Time) (_ *accounting.ProjectDailyUsage, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get daily usage by project ID")
	if err != nil {
		return nil, Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	usage, err := s.projectAccounting.GetProjectDailyUsageByDateRange(ctx, projectID, from, to, s.config.AsOfSystemTimeDuration)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return usage, nil
}

// GetProjectUsageLimits returns project limits and current usage.
//
// Among others,it can return one of the following errors returned by
// storj.io/storj/satellite/accounting.Service, wrapped Error.
func (s *Service) GetProjectUsageLimits(ctx context.Context, projectID uuid.UUID) (_ *ProjectUsageLimits, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get project usage limits", zap.String("projectID", projectID.String()))
	if err != nil {
		return nil, Error.Wrap(err)
	}

	_, err = s.isProjectMember(ctx, user.ID, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	prUsageLimits, err := s.getProjectUsageLimits(ctx, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	prObjectsSegments, err := s.projectAccounting.GetProjectObjectsSegments(ctx, projectID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return &ProjectUsageLimits{
		StorageLimit:   prUsageLimits.StorageLimit,
		BandwidthLimit: prUsageLimits.BandwidthLimit,
		StorageUsed:    prUsageLimits.StorageUsed,
		BandwidthUsed:  prUsageLimits.BandwidthUsed,
		ObjectCount:    prObjectsSegments.ObjectCount,
		SegmentCount:   prObjectsSegments.SegmentCount,
	}, nil
}

// GetTotalUsageLimits returns total limits and current usage for all the projects.
func (s *Service) GetTotalUsageLimits(ctx context.Context) (_ *ProjectUsageLimits, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := s.getUserAndAuditLog(ctx, "get total usage and limits for all the projects")
	if err != nil {
		return nil, Error.Wrap(err)
	}

	projects, err := s.store.Projects().GetOwn(ctx, user.ID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	var totalStorageLimit int64
	var totalBandwidthLimit int64
	var totalStorageUsed int64
	var totalBandwidthUsed int64

	for _, pr := range projects {
		prUsageLimits, err := s.getProjectUsageLimits(ctx, pr.ID)
		if err != nil {
			return nil, Error.Wrap(err)
		}

		totalStorageLimit += prUsageLimits.StorageLimit
		totalBandwidthLimit += prUsageLimits.BandwidthLimit
		totalStorageUsed += prUsageLimits.StorageUsed
		totalBandwidthUsed += prUsageLimits.BandwidthUsed
	}

	return &ProjectUsageLimits{
		StorageLimit:   totalStorageLimit,
		BandwidthLimit: totalBandwidthLimit,
		StorageUsed:    totalStorageUsed,
		BandwidthUsed:  totalBandwidthUsed,
	}, nil
}

func (s *Service) getProjectUsageLimits(ctx context.Context, projectID uuid.UUID) (_ *ProjectUsageLimits, err error) {
	defer mon.Task()(&ctx)(&err)

	storageLimit, err := s.projectUsage.GetProjectStorageLimit(ctx, projectID)
	if err != nil {
		return nil, err
	}
	bandwidthLimit, err := s.projectUsage.GetProjectBandwidthLimit(ctx, projectID)
	if err != nil {
		return nil, err
	}

	storageUsed, err := s.projectUsage.GetProjectStorageTotals(ctx, projectID)
	if err != nil {
		return nil, err
	}
	bandwidthUsed, err := s.projectUsage.GetProjectBandwidthTotals(ctx, projectID)
	if err != nil {
		return nil, err
	}

	return &ProjectUsageLimits{
		StorageLimit:   storageLimit.Int64(),
		BandwidthLimit: bandwidthLimit.Int64(),
		StorageUsed:    storageUsed,
		BandwidthUsed:  bandwidthUsed,
	}, nil
}

// TokenAuth returns an authenticated context by session token.
func (s *Service) TokenAuth(ctx context.Context, token consoleauth.Token, authTime time.Time) (_ context.Context, err error) {
	defer mon.Task()(&ctx)(&err)

	valid, err := s.tokens.ValidateToken(token)
	if err != nil {
		return nil, Error.Wrap(err)
	}
	if !valid {
		return nil, Error.New("incorrect signature")
	}

	sessionID, err := uuid.FromBytes(token.Payload)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	session, err := s.store.WebappSessions().GetBySessionID(ctx, sessionID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	ctx, err = s.authorize(ctx, session.UserID, session.ExpiresAt, authTime)
	if err != nil {
		err := errs.Combine(err, s.store.WebappSessions().DeleteBySessionID(ctx, sessionID))
		if err != nil {
			return nil, Error.Wrap(err)
		}
		return nil, err
	}

	return ctx, nil
}

// KeyAuth returns an authenticated context by api key.
func (s *Service) KeyAuth(ctx context.Context, apikey string, authTime time.Time) (_ context.Context, err error) {
	defer mon.Task()(&ctx)(&err)

	ctx = consoleauth.WithAPIKey(ctx, []byte(apikey))

	userID, exp, err := s.restKeys.GetUserAndExpirationFromKey(ctx, apikey)
	if err != nil {
		return nil, err
	}

	ctx, err = s.authorize(ctx, userID, exp, authTime)
	if err != nil {
		return nil, err
	}

	return ctx, nil
}

// checkProjectCanBeDeleted ensures that all data, api-keys and buckets are deleted and usage has been accounted.
// no error means the project status is clean.
func (s *Service) checkProjectCanBeDeleted(ctx context.Context, user *User, projectID uuid.UUID) (err error) {
	defer mon.Task()(&ctx)(&err)

	buckets, err := s.buckets.CountBuckets(ctx, projectID)
	if err != nil {
		return err
	}
	if buckets > 0 {
		return ErrUsage.New("some buckets still exist")
	}

	keys, err := s.store.APIKeys().GetPagedByProjectID(ctx, projectID, APIKeyCursor{Limit: 1, Page: 1})
	if err != nil {
		return err
	}
	if keys.TotalCount > 0 {
		return ErrUsage.New("some api keys still exist")
	}

	if user.PaidTier {
		err = s.Payments().checkProjectUsageStatus(ctx, projectID)
		if err != nil {
			return ErrUsage.Wrap(err)
		}
	}

	err = s.Payments().checkProjectInvoicingStatus(ctx, projectID)
	if err != nil {
		return ErrUsage.Wrap(err)
	}

	return nil
}

// checkProjectLimit is used to check if user is able to create a new project.
func (s *Service) checkProjectLimit(ctx context.Context, userID uuid.UUID) (currentProjects int, err error) {
	defer mon.Task()(&ctx)(&err)

	limit, err := s.store.Users().GetProjectLimit(ctx, userID)
	if err != nil {
		return 0, Error.Wrap(err)
	}

	projects, err := s.GetUsersProjects(ctx)
	if err != nil {
		return 0, Error.Wrap(err)
	}

	if len(projects) >= limit {
		return 0, ErrProjLimit.New(projLimitErrMsg)
	}

	return len(projects), nil
}

// getUserProjectLimits is a method to get the users storage and bandwidth limits for new projects.
func (s *Service) getUserProjectLimits(ctx context.Context, userID uuid.UUID) (_ *UserProjectLimits, err error) {
	defer mon.Task()(&ctx)(&err)

	result, err := s.store.Users().GetUserProjectLimits(ctx, userID)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return &UserProjectLimits{
		StorageLimit:   result.ProjectStorageLimit,
		BandwidthLimit: result.ProjectBandwidthLimit,
		SegmentLimit:   result.ProjectSegmentLimit,
	}, nil
}

// CreateRegToken creates new registration token. Needed for testing.
func (s *Service) CreateRegToken(ctx context.Context, projLimit int) (_ *RegistrationToken, err error) {
	defer mon.Task()(&ctx)(&err)
	result, err := s.store.RegistrationTokens().Create(ctx, projLimit)
	if err != nil {
		return nil, Error.Wrap(err)
	}

	return result, nil
}

// authorize returns an authorized context by user ID.
func (s *Service) authorize(ctx context.Context, userID uuid.UUID, expiration time.Time, authTime time.Time) (_ context.Context, err error) {
	defer mon.Task()(&ctx)(&err)
	if !expiration.IsZero() && expiration.Before(authTime) {
		return nil, ErrTokenExpiration.New("authorization failed. expiration reached.")
	}

	user, err := s.store.Users().Get(ctx, userID)
	if err != nil {
		return nil, Error.New("authorization failed. no user with id: %s", userID.String())
	}

	if user.Status != Active {
		return nil, Error.New("authorization failed. no active user with id: %s", userID.String())
	}
	return WithUser(ctx, user), nil
}

// isProjectMember is return type of isProjectMember service method.
type isProjectMember struct {
	project    *Project
	membership *ProjectMember
}

// isProjectOwner checks if the user is an owner of a project.
func (s *Service) isProjectOwner(ctx context.Context, userID uuid.UUID, projectID uuid.UUID) (isOwner bool, err error) {
	defer mon.Task()(&ctx)(&err)
	project, err := s.store.Projects().Get(ctx, projectID)
	if err != nil {
		return false, err
	}

	if project.OwnerID != userID {
		return false, ErrUnauthorized.New(unauthorizedErrMsg)
	}

	return true, nil
}

// isProjectMember checks if the user is a member of given project.
func (s *Service) isProjectMember(ctx context.Context, userID uuid.UUID, projectID uuid.UUID) (_ isProjectMember, err error) {
	defer mon.Task()(&ctx)(&err)
	project, err := s.store.Projects().Get(ctx, projectID)
	if err != nil {
		return isProjectMember{}, Error.Wrap(err)
	}

	memberships, err := s.store.ProjectMembers().GetByMemberID(ctx, userID)
	if err != nil {
		return isProjectMember{}, Error.Wrap(err)
	}

	membership, ok := findMembershipByProjectID(memberships, projectID)
	if ok {
		return isProjectMember{
			project:    project,
			membership: &membership,
		}, nil
	}

	return isProjectMember{}, ErrNoMembership.New(unauthorizedErrMsg)
}

// WalletInfo contains all the information about a destination wallet assigned to a user.
type WalletInfo struct {
	Address blockchain.Address `json:"address"`
	Balance *big.Int           `json:"balance"`
}

// TokenTransactions represents the list of ERC-20 token payments.
type TokenTransactions struct {
	Transactions []TokenTransaction `json:"transactions"`
}

// TokenTransaction contains the data of one single ERC-20 token payment.
type TokenTransaction struct {
	Origin  blockchain.Address `json:"origin"`
	Address blockchain.Address `json:"address"`
	TxHash  blockchain.Hash    `json:"tx_hash"`
	Status  string             `json:"status"`
	Date    time.Time          `json:"date"`
	Amount  monetary.Amount    `json:"amount"`
}

// ErrWalletNotClaimed shows that no address is claimed by the user.
var ErrWalletNotClaimed = errs.Class("wallet is not claimed")

// ClaimWallet requests a new wallet for the users to be used for payments. If wallet is already claimed,
// it will return with the info without error.
func (payment Payments) ClaimWallet(ctx context.Context) (_ WalletInfo, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := payment.service.getUserAndAuditLog(ctx, "claim wallet")
	if err != nil {
		return WalletInfo{}, Error.Wrap(err)
	}
	address, err := payment.service.depositWallets.Claim(ctx, user.ID)
	if err != nil {
		return WalletInfo{}, Error.Wrap(err)
	}
	return WalletInfo{
		Address: address,
		Balance: nil, //TODO: populate with call to billing table
	}, nil
}

// GetWallet returns with the assigned wallet, or with ErrWalletNotClaimed if not yet claimed.
func (payment Payments) GetWallet(ctx context.Context) (_ WalletInfo, err error) {
	defer mon.Task()(&ctx)(&err)

	user, err := GetUser(ctx)
	if err != nil {
		return WalletInfo{}, Error.Wrap(err)
	}
	address, err := payment.service.depositWallets.Get(ctx, user.ID)
	if err != nil {
		return WalletInfo{}, Error.Wrap(err)
	}
	return WalletInfo{
		Address: address,
		Balance: nil, //TODO: populate with call to billing table
	}, nil
}

// Transactions returns with all the native blockchain transactions.
func (payment Payments) Transactions(ctx context.Context) (TokenTransactions, error) {
	panic("Not yet implemented")
}

func findMembershipByProjectID(memberships []ProjectMember, projectID uuid.UUID) (ProjectMember, bool) {
	for _, membership := range memberships {
		if membership.ProjectID == projectID {
			return membership, true
		}
	}
	return ProjectMember{}, false
}

// DeleteSessionByToken removes the session corresponding to the given token from the database.
func (s *Service) DeleteSessionByToken(ctx context.Context, token consoleauth.Token) (err error) {
	defer mon.Task()(&ctx)(&err)

	valid, err := s.tokens.ValidateToken(token)
	if err != nil {
		return err
	}
	if !valid {
		return ErrValidation.New("Invalid session token.")
	}

	id, err := uuid.FromBytes(token.Payload)
	if err != nil {
		return err
	}

	return s.store.WebappSessions().DeleteBySessionID(ctx, id)
}
