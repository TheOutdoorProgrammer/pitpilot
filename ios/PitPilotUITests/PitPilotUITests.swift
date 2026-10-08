import XCTest

final class PitPilotUITests: XCTestCase {
    override func setUpWithError() throws { continueAfterFailure = false }
    func testConnectionValidationAndGarageJourney() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-testing"]
        app.launch()
        connect(app)
        XCTAssertTrue(app.buttons["Add a vehicle"].waitForExistence(timeout: 5))
        capture("Empty garage", app)
        let add = app.navigationBars.buttons["Add vehicle"]
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "isHittable == true"), object: add)], timeout: 5), .completed)
        add.tap()
        XCTAssertTrue(app.textFields["vehicleName"].waitForExistence(timeout: 5))
        app.textFields["vehicleName"].tap()
        app.textFields["vehicleName"].typeText("Weekend truck")
        app.buttons["Save"].tap()
        XCTAssertTrue(app.textFields["vehicleName"].waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Weekend truck"].waitForExistence(timeout: 5))
        capture("Garage", app)
        app.staticTexts["Weekend truck"].tap()
        XCTAssertTrue(app.buttons["Log entry"].waitForExistence(timeout: 5))
        app.buttons["Log entry"].tap()
        app.textFields["What did you do?"].tap()
        app.textFields["What did you do?"].typeText("Oil and filter")
        app.buttons["Save"].tap()
        XCTAssertTrue(app.textFields["What did you do?"].waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Oil and filter"].waitForExistence(timeout: 5))
        capture("Vehicle history", app)
        app.buttons["Upcoming"].tap()
        app.buttons["Add reminder"].tap()
        app.textFields["Reminder title"].tap()
        app.textFields["Reminder title"].typeText("Rotate tires")
        app.buttons["Save"].tap()
        XCTAssertTrue(app.textFields["Reminder title"].waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Complete Rotate tires"].waitForExistence(timeout: 5))
        app.buttons["Complete Rotate tires"].tap()
        XCTAssertTrue(app.buttons["Mark Rotate tires incomplete"].waitForExistence(timeout: 5))
        capture("Completed reminder", app)
        app.buttons["Trips"].tap()
        XCTAssertTrue(app.staticTexts["No recorded trips yet"].waitForExistence(timeout: 3))
        capture("Trips empty state", app)
    }

    func testRecordedRouteAndOfflineCache() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-testing", "--ui-testing-populated"]
        app.launch()
        connect(app)
        XCTAssertTrue(app.staticTexts["Synthetic route truck"].waitForExistence(timeout: 5))
        app.staticTexts["Synthetic route truck"].tap()
        XCTAssertTrue(app.staticTexts["Synthetic oil service"].waitForExistence(timeout: 5))
        app.buttons["Trips"].tap()
        XCTAssertTrue(app.staticTexts["Synthetic park loop"].waitForExistence(timeout: 5))
        app.staticTexts["Synthetic park loop"].tap()
        XCTAssertTrue(app.staticTexts["4 recorded location samples"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "Recorded trip route")).firstMatch.waitForExistence(timeout: 5))
        capture("Synthetic recorded route", app)

        app.terminate()
        app.launchArguments = ["--ui-testing", "--ui-testing-preserve-cache", "--ui-testing-offline"]
        app.launch()
        XCTAssertTrue(app.staticTexts["Saved on this phone"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Synthetic route truck"].exists)
        XCTAssertFalse(app.navigationBars.buttons["Add vehicle"].isEnabled)
        capture("Offline garage cache", app)
        app.staticTexts["Synthetic route truck"].tap()
        XCTAssertTrue(app.staticTexts["Synthetic oil service"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["Log entry"].isEnabled)
        XCTAssertFalse(app.buttons["Add to vehicle"].isEnabled)
        capture("Offline vehicle history", app)
        app.buttons["Upcoming"].tap()
        XCTAssertTrue(app.staticTexts["Synthetic tire inspection"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["Complete Synthetic tire inspection"].isEnabled)
        XCTAssertFalse(app.buttons["Add reminder"].isEnabled)
    }

    private func connect(_ app: XCUIApplication) {
        let button = app.buttons["connectButton"]
        XCTAssertFalse(button.isEnabled)
        app.textFields["serverURL"].tap()
        app.textFields["serverURL"].typeText("https://pitpilot.test")
        app.secureTextFields["apiToken"].tap()
        app.secureTextFields["apiToken"].typeText("test-token")
        app.swipeUp()
        button.tap()
        XCTAssertTrue(app.navigationBars.buttons["Add vehicle"].waitForExistence(timeout: 5))
        let passwordService = XCUIApplication(bundleIdentifier: "com.apple.SafariViewService")
        if passwordService.buttons["Not Now"].waitForExistence(timeout: 3) {
            passwordService.buttons["Not Now"].tap()
            XCTAssertTrue(passwordService.buttons["Not Now"].waitForNonExistence(timeout: 5))
            app.activate()
        }
    }
    private func capture(_ name: String, _ app: XCUIApplication) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
