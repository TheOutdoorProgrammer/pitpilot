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

    func testImportedNoteSearchEditAndMetadata() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-testing", "--ui-testing-migration"]
        app.launch()
        connect(app)
        app.staticTexts["Synthetic route truck"].tap()
        let information = app.buttons["vehicleInformation"]
        XCTAssertTrue(information.waitForExistence(timeout: 5))
        information.tap()
        XCTAssertTrue(app.staticTexts["SYNTHETIC-VIN"].exists)
        XCTAssertTrue(app.staticTexts["TEST-ONLY"].exists)
        XCTAssertTrue(app.staticTexts["Synthetic region"].exists)
        capture("Imported vehicle metadata", app)
        information.tap()
        let search = app.textFields["historySearch"]
        XCTAssertTrue(search.waitForExistence(timeout: 5))
        search.tap()
        search.typeText("collector journal\n")
        XCTAssertTrue(app.staticTexts["Synthetic collector journal"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["Synthetic daily reading"].exists)
        app.staticTexts["Synthetic collector journal"].tap()
        XCTAssertTrue(app.staticTexts["Undated"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["USD"].exists)
        XCTAssertTrue(app.staticTexts["Migration acceptance note"].exists)
        capture("Imported undated note", app)
        app.buttons["Edit record"].tap()
        let notes = app.descendants(matching: .any).matching(identifier: "recordNotes").firstMatch
        XCTAssertTrue(notes.waitForExistence(timeout: 5))
        notes.tap()
        notes.typeText(" corrected")
        app.buttons["Save"].tap()
        let corrected = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "corrected")).firstMatch
        XCTAssertTrue(corrected.waitForExistence(timeout: 5))
        app.swipeUp()
        XCTAssertTrue(app.staticTexts["Synthetic sensor"].exists)
        XCTAssertTrue(app.staticTexts["Imported from LubeLogger"].exists)
        capture("Edited imported note with provenance", app)
    }

    func testImportedReadingPlanAndRecurringReminder() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-testing", "--ui-testing-migration"]
        app.launch()
        connect(app)
        app.staticTexts["Synthetic route truck"].tap()
        if !app.staticTexts["Synthetic daily reading"].isHittable { app.swipeUp() }
        XCTAssertTrue(app.staticTexts["Synthetic daily reading"].waitForExistence(timeout: 5))
        app.staticTexts["Synthetic daily reading"].tap()
        XCTAssertTrue(app.staticTexts["Estimated reading"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Initial reading"].exists)
        XCTAssertTrue(app.staticTexts["Final reading"].exists)
        capture("Imported estimated odometer", app)
        app.navigationBars.buttons.element(boundBy: 0).tap()
        if !app.buttons["Upcoming"].isHittable { app.swipeDown() }
        app.buttons["Upcoming"].tap()
        XCTAssertTrue(app.staticTexts["Synthetic planned repair"].waitForExistence(timeout: 5))
        app.staticTexts["Synthetic planned repair"].tap()
        XCTAssertTrue(app.staticTexts["Critical"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Planned"].exists)
        capture("Imported planned work", app)
        let linkedReminder = app.buttons["Synthetic recurring oil"]
        if !linkedReminder.isHittable { app.swipeUp() }
        XCTAssertTrue(linkedReminder.waitForExistence(timeout: 5))
        linkedReminder.tap()
        XCTAssertTrue(app.staticTexts["Due reading"].waitForExistence(timeout: 5))
        app.navigationBars.buttons.element(boundBy: 0).tap()
        app.navigationBars.buttons.element(boundBy: 0).tap()
        app.swipeUp()
        let complete = app.buttons["Complete Synthetic recurring oil"]
        XCTAssertTrue(complete.waitForExistence(timeout: 5))
        complete.tap()
        XCTAssertFalse(app.buttons["Complete"].isEnabled)
        let miles = app.textFields["completionMileage"]
        miles.tap()
        miles.typeText("125100")
        app.buttons["Complete"].tap()
        XCTAssertTrue(miles.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["At 130,100 mi"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["Complete Synthetic recurring oil"].exists)
        capture("Recurring reminder advanced once", app)
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
