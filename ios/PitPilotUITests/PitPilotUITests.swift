import XCTest

final class PitPilotUITests: XCTestCase {
    override func setUpWithError() throws { continueAfterFailure = false }
    func testConnectionValidationAndGarageJourney() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-testing"]
        app.launch()
        let connect = app.buttons["connectButton"]
        XCTAssertFalse(connect.isEnabled)
        app.textFields["serverURL"].tap()
        app.textFields["serverURL"].typeText("https://pitpilot.test")
        app.secureTextFields["apiToken"].tap()
        app.secureTextFields["apiToken"].typeText("test-token")
        app.swipeUp()
        connect.tap()
        XCTAssertTrue(app.buttons["Add a vehicle"].waitForExistence(timeout: 5))
        let passwordService = XCUIApplication(bundleIdentifier: "com.apple.SafariViewService")
        if passwordService.buttons["Not Now"].waitForExistence(timeout: 3) {
            passwordService.buttons["Not Now"].tap()
            XCTAssertTrue(passwordService.buttons["Not Now"].waitForNonExistence(timeout: 5))
            app.activate()
        }
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
        app.buttons["Trips"].tap()
        XCTAssertTrue(app.staticTexts["No recorded trips yet"].waitForExistence(timeout: 3))
        capture("Trips empty state", app)
    }
    private func capture(_ name: String, _ app: XCUIApplication) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
