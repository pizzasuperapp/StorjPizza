// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package satellite_test

import (
	"testing"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
	"storj.io/storj/private/testplanet"
	"storj.io/storj/testsuite/ui/uitest"
)

func TestOnboarding_WizardBrowser(t *testing.T) {
	uitest.Edge(t, func(t *testing.T, ctx *testcontext.Context, planet *uitest.EdgePlanet, browser *rod.Browser) {
		page := openPage(browser, planet.Satellites[0].ConsoleURL())

		// Sign up and login.
		signUpWithUser(t, planet, page)
		loginWithUser(t, planet, page)

		// testing onboarding workflow browser
		page.MustElementR("span", "Continue in web ->").MustClick()
		waitVueTick(page)

		// Buckets Page
		bucketsTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, bucketsTitle, "Create a bucket")
		page.MustElementR("[aria-roledescription=title]", "Create a bucket")
		page.MustElementR("span", "Continue").MustClick()
		waitVueTick(page)
		page.MustElementR("[aria-roledescription=title]", "Encrypt your bucket")
		page.MustElementR("span", "Continue").MustClick()
		waitVueTick(page)
		page.MustElementR("[aria-roledescription=title]", "Generate a passphrase")
		mnemonic := page.MustElement("[aria-roledescription=mnemonic]").MustText()
		require.NotEmpty(t, mnemonic)
		page.MustElementR("span", "Back").MustClick()
		waitVueTick(page)
		page.MustElementR("[aria-roledescription=title]", "Encrypt your bucket")
		page.MustElement("[aria-roledescription=manual]").MustClick()
		page.MustElementR("span", "Continue").MustClick()
		waitVueTick(page)
		page.MustElementR("[aria-roledescription=title]", "Enter a passphrase")
		page.MustElement("[aria-roledescription=passphrase] input").MustInput("1")
		page.MustElementR("label", "I understand, and I have saved the passphrase.").MustClick()
		page.MustElementR("span", "Continue").MustClick()

		// Verify that browser component has loaded and that the dropzone is present
		page.MustElementR("p", "Drop Files Here to Upload")
	})
}

func TestOnboarding_WizardCLIFlow(t *testing.T) {
	uitest.Run(t, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet, browser *rod.Browser) {
		signupPageURL := planet.Satellites[0].ConsoleURL() + "/signup"
		fullName := "John Doe"
		emailAddress := "test@email.test"
		password := "qazwsx123"

		page := openPage(browser, signupPageURL)

		// First time User signup
		page.MustElement("[aria-roledescription=name] input").MustInput(fullName)
		page.MustElement("[aria-roledescription=email] input").MustInput(emailAddress)
		page.MustElement("[aria-roledescription=password] input").MustInput(password)
		page.MustElement("[aria-roledescription=retype-password] input").MustInput(password)
		page.MustElement(".checkmark").MustClick()
		page.Keyboard.MustPress(input.Enter)
		waitVueTick(page)

		confirmAccountEmailMessage := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, confirmAccountEmailMessage, "You're almost there!")

		// First time User log in
		page.MustElement("[href=\"/login\"]").MustClick()
		page.MustElement("[aria-roledescription=email] input").MustInput(emailAddress)
		page.MustElement("[aria-roledescription=password] input").MustInput(password)
		page.Keyboard.MustPress(input.Enter)
		waitVueTick(page)

		// Testing onboarding workflow uplinkCLI method
		// Welcome screen
		page.MustElementR("span", "Continue in cli ->").MustClick()
		waitVueTick(page)

		// Create Access Grant screen
		page.MustElementR("[aria-roledescription=title]", "Create an Access Grant")
		page.MustElement("[aria-roledescription=name] input").MustInput("name")
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)

		// Access Grant permissions screen
		page.MustElementR("[aria-roledescription=title]", "Access Permissions")
		page.MustElement("[aria-roledescription=select-permissions]").MustClick()
		page.MustElementR("label", "Download").MustClick()
		page.MustElementR("label", "Upload").MustClick()
		page.MustElementR("label", "List").MustClick()
		page.MustElementR("label", "Delete").MustClick()
		page.MustElement("[aria-roledescription=select-permissions]").MustClick()
		page.MustElement("[aria-roledescription=select-duration]").MustClick()
		page.MustElementR("li", "6 Months").MustClick()
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)

		// API key generated screen
		apiKeyGeneratedTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, apiKeyGeneratedTitle, "API Key Generated")
		address := page.MustElement("[aria-roledescription=satellite-address]").MustText()
		require.NotEmpty(t, address)
		key := page.MustElement("[aria-roledescription=api-key]").MustText()
		require.NotEmpty(t, key)
		page.MustElementR("span", "Back").MustClick()
		waitVueTick(page)
		permissionsTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, permissionsTitle, "Access Permissions")
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)

		// Install Uplink CLI screen
		cliInstallTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, cliInstallTitle, "Install Uplink CLI")

		// OS tabs
		page.MustElement("[aria-roledescription=windows]").MustClick()
		windowsBinaryLink, err := page.MustElementX("(//a[text()=\" Windows Uplink Binary \"])").Attribute("href")
		require.NoError(t, err)
		require.Equal(t, *windowsBinaryLink, "https://github.com/storj/storj/releases/latest/download/uplink_windows_amd64.zip")
		page.MustElement("[aria-roledescription=linux]").MustClick()
		linuxAMDBinaryLink, err := page.MustElementX("(//a[text()=\" Linux AMD64 Uplink Binary \"])").Attribute("href")
		require.NoError(t, err)
		require.Equal(t, *linuxAMDBinaryLink, "https://github.com/storj/storj/releases/latest/download/uplink_linux_amd64.zip")
		linuxARMBinaryLink, err := page.MustElementX("(//a[text()=\" Linux ARM Uplink Binary \"])").Attribute("href")
		require.NoError(t, err)
		require.Equal(t, *linuxARMBinaryLink, "https://github.com/storj/storj/releases/latest/download/uplink_linux_arm.zip")
		page.MustElement("[aria-roledescription=macos]").MustClick()
		macOSBinaryLink, err := page.MustElementX("(//a[text()=\" macOS Uplink Binary \"])").Attribute("href")
		require.NoError(t, err)
		require.Equal(t, *macOSBinaryLink, "https://github.com/storj/storj/releases/latest/download/uplink_darwin_amd64.zip")

		// Back and forth click test
		page.MustElementR("span", "Back").MustClick()
		waitVueTick(page)
		apiKeyGeneratedTitle1 := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, apiKeyGeneratedTitle1, "API Key Generated")
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)

		// CLI setup screen
		cliSetupTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, cliSetupTitle, "Uplink setup")

		// OS tabs
		page.MustElement("[aria-roledescription=windows]").MustClick()
		windowsCLISetupCmd := page.MustElement("[aria-roledescription=windows-cli-setup]").MustText()
		require.Equal(t, "./uplink.exe setup", windowsCLISetupCmd)
		page.MustElement("[aria-roledescription=linux]").MustClick()
		linuxCLISetupCmd := page.MustElement("[aria-roledescription=linux-cli-setup]").MustText()
		require.Equal(t, "uplink setup", linuxCLISetupCmd)
		page.MustElement("[aria-roledescription=macos]").MustClick()
		macosCLISetupCmd := page.MustElement("[aria-roledescription=macos-cli-setup]").MustText()
		require.Equal(t, "uplink setup", macosCLISetupCmd)

		// Back and forth click test
		page.MustElementR("span", "Back").MustClick()
		waitVueTick(page)
		cliInstallTitle1 := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, cliInstallTitle1, "Install Uplink CLI")
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)

		// Create bucket screen
		createBucketTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, createBucketTitle, "Create a bucket")

		// OS tabs
		page.MustElement("[aria-roledescription=windows]").MustClick()
		windowsCreateBucketCmd := page.MustElement("[aria-roledescription=windows-create-bucket]").MustText()
		require.Equal(t, "./uplink.exe mb sj://cakes", windowsCreateBucketCmd)
		page.MustElement("[aria-roledescription=linux]").MustClick()
		linuxCreateBucketCmd := page.MustElement("[aria-roledescription=linux-create-bucket]").MustText()
		require.Equal(t, "uplink mb sj://cakes", linuxCreateBucketCmd)
		page.MustElement("[aria-roledescription=macos]").MustClick()
		macosCreateBucketCmd := page.MustElement("[aria-roledescription=macos-create-bucket]").MustText()
		require.Equal(t, "uplink mb sj://cakes", macosCreateBucketCmd)

		// Back and forth click test
		page.MustElementR("span", "Back").MustClick()
		waitVueTick(page)
		cliSetupTitle1 := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, cliSetupTitle1, "Uplink setup")
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)

		// Ready to upload screen
		readyToUploadTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, readyToUploadTitle, "Ready to upload")

		// OS tabs
		page.MustElement("[aria-roledescription=windows]").MustClick()
		windowsUploadCmd := page.MustElement("[aria-roledescription=windows-upload]").MustText()
		require.Equal(t, "./uplink.exe cp <FILE_PATH> sj://cakes", windowsUploadCmd)
		page.MustElement("[aria-roledescription=linux]").MustClick()
		linuxUploadCmd := page.MustElement("[aria-roledescription=linux-upload]").MustText()
		require.Equal(t, "uplink cp ~/Desktop/cheesecake.jpg sj://cakes", linuxUploadCmd)
		page.MustElement("[aria-roledescription=macos]").MustClick()
		macosUploadCmd := page.MustElement("[aria-roledescription=macos-upload]").MustText()
		require.Equal(t, "uplink cp ~/Desktop/cheesecake.jpg sj://cakes", macosUploadCmd)

		// Back and forth click test
		page.MustElementR("span", "Back").MustClick()
		waitVueTick(page)
		createBucketTitle1 := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, createBucketTitle1, "Create a bucket")
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)

		// List a bucket screen
		listTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, listTitle, "Listing a bucket")

		// OS tabs
		page.MustElement("[aria-roledescription=windows]").MustClick()
		windowsListCmd := page.MustElement("[aria-roledescription=windows-list]").MustText()
		require.Equal(t, "./uplink.exe ls sj://cakes", windowsListCmd)
		page.MustElement("[aria-roledescription=linux]").MustClick()
		linuxListCmd := page.MustElement("[aria-roledescription=linux-list]").MustText()
		require.Equal(t, "uplink ls sj://cakes", linuxListCmd)
		page.MustElement("[aria-roledescription=macos]").MustClick()
		macosListCmd := page.MustElement("[aria-roledescription=macos-list]").MustText()
		require.Equal(t, "uplink ls sj://cakes", macosListCmd)

		// Back and forth click test
		page.MustElementR("span", "Back").MustClick()
		waitVueTick(page)
		readyToUploadTitle1 := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, readyToUploadTitle1, "Ready to upload")
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)

		// Download screen
		downloadTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, downloadTitle, "Download")

		// OS tabs
		page.MustElement("[aria-roledescription=windows]").MustClick()
		windowsDownloadCmd := page.MustElement("[aria-roledescription=windows-download]").MustText()
		require.Equal(t, "./uplink.exe cp sj://cakes/cheesecake.jpg <DESTINATION_PATH>/cheesecake.jpg", windowsDownloadCmd)
		page.MustElement("[aria-roledescription=linux]").MustClick()
		linuxDownloadCmd := page.MustElement("[aria-roledescription=linux-download]").MustText()
		require.Equal(t, "uplink cp sj://cakes/cheesecake.jpg ~/Downloads/cheesecake.jpg", linuxDownloadCmd)
		page.MustElement("[aria-roledescription=macos]").MustClick()
		macosDownloadCmd := page.MustElement("[aria-roledescription=macos-download]").MustText()
		require.Equal(t, "uplink cp sj://cakes/cheesecake.jpg ~/Downloads/cheesecake.jpg", macosDownloadCmd)

		// Back and forth click test
		page.MustElementR("span", "Back").MustClick()
		waitVueTick(page)
		listTitle1 := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, listTitle1, "Listing a bucket")
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)

		// Share link screen
		shareLinkTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, shareLinkTitle, "Share a link")

		// OS tabs
		page.MustElement("[aria-roledescription=windows]").MustClick()
		windowsShareCmd := page.MustElement("[aria-roledescription=windows-share]").MustText()
		require.Equal(t, "./uplink.exe share --url sj://cakes/cheesecake.jpg", windowsShareCmd)
		page.MustElement("[aria-roledescription=linux]").MustClick()
		linuxShareCmd := page.MustElement("[aria-roledescription=linux-share]").MustText()
		require.Equal(t, "uplink share --url sj://cakes/cheesecake.jpg", linuxShareCmd)
		page.MustElement("[aria-roledescription=macos]").MustClick()
		macosShareCmd := page.MustElement("[aria-roledescription=macos-share]").MustText()
		require.Equal(t, "uplink share --url sj://cakes/cheesecake.jpg", macosShareCmd)

		// Back and forth click test
		page.MustElementR("span", "Back").MustClick()
		waitVueTick(page)
		downloadTitle1 := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, downloadTitle1, "Download")
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)
		page.MustElementR("span", "Continue ->").MustClick()
		waitVueTick(page)

		// Success screen
		successTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, successTitle, "Wonderful")
		page.MustElementR("span", "Finish").MustClick()
		waitVueTick(page)
		dashboardTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, dashboardTitle, "Dashboard")
		page.MustNavigateBack()
		successTitle1 := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, successTitle1, "Wonderful")
	})
}

func TestOnboarding_WelcomeScreenEncryption(t *testing.T) {
	uitest.Run(t, func(t *testing.T, ctx *testcontext.Context, planet *testplanet.Planet, browser *rod.Browser) {
		signupPageURL := planet.Satellites[0].ConsoleURL() + "/signup"
		fullName := "John Doe"
		emailAddress := "test@email.test"
		password := "qazwsx123"

		page := openPage(browser, signupPageURL)

		// First time User signup
		page.MustElement("[aria-roledescription=name] input").MustInput(fullName)
		page.MustElement("[aria-roledescription=email] input").MustInput(emailAddress)
		page.MustElement("[aria-roledescription=password] input").MustInput(password)
		page.MustElement("[aria-roledescription=retype-password] input").MustInput(password)
		page.MustElement(".checkmark").MustClick()
		page.Keyboard.MustPress(input.Enter)
		waitVueTick(page)

		confirmAccountEmailMessage := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, confirmAccountEmailMessage, "You're almost there!")

		// Login as first time User
		page.MustElement("[href=\"/login\"]").MustClick()
		page.MustElement("[aria-roledescription=email] input").MustInput(emailAddress)
		page.MustElement("[aria-roledescription=password] input").MustInput(password)
		page.Keyboard.MustPress(input.Enter)
		waitVueTick(page)

		// Welcome screen encryption test
		welcomeTitle := page.MustElement("[aria-roledescription=title]").MustText()
		require.Contains(t, welcomeTitle, "Welcome")
		serverSideEncTitle := page.MustElement("[aria-roledescription=server-side-encryption-title]").MustText()
		require.Contains(t, serverSideEncTitle, "SERVER-SIDE ENCRYPTED")
		endToEndEncTitle := page.MustElement("[aria-roledescription=end-to-end-encryption-title]").MustText()
		require.Contains(t, endToEndEncTitle, "END-TO-END ENCRYPTED")
		serverSideEncLink, err := page.MustElement("[aria-roledescription=server-side-encryption-link]").Attribute("href")
		require.NoError(t, err)
		require.Equal(t, "https://docs.storj.io/concepts/encryption-key/design-decision-server-side-encryption", *serverSideEncLink)
		endToEndEncLink, err := page.MustElement("[aria-roledescription=end-to-end-encryption-link]").Attribute("href")
		require.NoError(t, err)
		require.Equal(t, "https://docs.storj.io/concepts/encryption-key/design-decision-end-to-end-encryption", *endToEndEncLink)
	})
}
