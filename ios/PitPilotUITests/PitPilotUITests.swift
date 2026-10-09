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
        app.buttons["History"].tap()
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
        app.buttons["History"].tap()
        XCTAssertTrue(app.staticTexts["Synthetic oil service"].waitForExistence(timeout: 5))
        app.buttons["Trips"].tap()
        reveal(app.staticTexts["Synthetic park loop"], app)
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
        app.buttons["History"].tap()
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
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Estimated odometer")).firstMatch.exists)
        XCTAssertTrue(app.staticTexts["VIN, SYNTHETIC-VIN"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["License plate, TEST-ONLY"].exists)
        XCTAssertTrue(app.staticTexts["Synthetic region"].exists)
        XCTAssertFalse(app.staticTexts["Source record: 1"].exists)
        XCTAssertFalse(app.staticTexts["Imported from LubeLogger"].exists)
        capture("Imported vehicle metadata", app)
        reveal(app.buttons["History"], app)
        app.buttons["History"].tap()
        let search = app.textFields["historySearch"]
        XCTAssertTrue(search.waitForExistence(timeout: 5))
        search.tap()
        search.typeText("collector journal\n")
        XCTAssertTrue(app.staticTexts["Synthetic collector journal"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.staticTexts["Synthetic daily reading"].exists)
        app.staticTexts["Synthetic collector journal"].tap()
        XCTAssertTrue(app.staticTexts["Date, Undated"].waitForExistence(timeout: 5))
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
        app.buttons["History"].tap()
        if !app.staticTexts["Synthetic daily reading"].isHittable { app.swipeUp() }
        XCTAssertTrue(app.staticTexts["Synthetic daily reading"].waitForExistence(timeout: 5))
        app.staticTexts["Synthetic daily reading"].tap()
        XCTAssertTrue(app.staticTexts["Estimated reading"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Initial reading"].exists)
        XCTAssertTrue(app.staticTexts["Final reading"].exists)
        capture("Imported estimated odometer", app)
        app.buttons["Edit record"].tap()
        let includeTime = app.switches["recordIncludeTime"]
        XCTAssertTrue(includeTime.waitForExistence(timeout: 5))
        XCTAssertEqual(includeTime.value as? String, "0")
        capture("Imported odometer preserves date-only precision", app)
        includeTime.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)).tap()
        XCTAssertEqual(includeTime.value as? String, "1")
        XCTAssertTrue(app.datePickers["recordDate"].exists)
        capture("Odometer form with optional capture time", app)
        includeTime.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)).tap()
        XCTAssertEqual(includeTime.value as? String, "0")
        app.buttons["Cancel"].tap()
        app.navigationBars.buttons.element(boundBy: 0).tap()
        capture("Odometer history after cancelling date edit", app)
        if !app.buttons["Upcoming"].isHittable { app.swipeDown() }
        app.buttons["Upcoming"].tap()
        XCTAssertTrue(app.staticTexts["Synthetic planned repair"].waitForExistence(timeout: 5))
        app.staticTexts["Synthetic planned repair"].tap()
        XCTAssertTrue(app.staticTexts["Priority, Critical"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Status, Planned"].exists)
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

    func testNativeSignalsAndFaithfulHistory() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-testing", "--ui-testing-signals"]
        app.launch()
        connect(app)
        app.staticTexts["Synthetic route truck"].tap()
        let fuel = app.buttons["signal-fuel_level_pct"]
        XCTAssertTrue(fuel.waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["signal-rpm"].exists)
        if !fuel.isHittable { app.swipeUp() }
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in fuel.label.contains("all sources") }, object: nil)], timeout: 5), .completed)
        capture("Signals overview with actual values", app)
        fuel.tap()
        XCTAssertTrue(app.descendants(matching: .any)["signalHistoryChart"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["One timeline · squares have day precision · dotted gaps"].exists)
        XCTAssertFalse(app.descendants(matching: .any)["calendarSignalChart"].exists)
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@ AND label CONTAINS %@ AND label CONTAINS %@", "LubeLogger", "Smartcar", "Pi")).firstMatch.exists)
        capture("Unified fuel chart with dense calendar and timestamped sources", app)
        XCUIDevice.shared.orientation = .landscapeLeft
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in app.frame.width > app.frame.height }, object: nil)], timeout: 5), .completed)
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.8)).press(forDuration: 0.1,
            thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.4)))
        capture("Fuel chart landscape", app)
        XCUIDevice.shared.orientation = .portrait
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in app.frame.width < app.frame.height }, object: nil)], timeout: 5), .completed)
        reveal(app.staticTexts["What this measures"], app)
        XCTAssertTrue(app.staticTexts["Fuel remaining as a percentage of tank capacity."].exists)
        capture("Metric explanation and interpretation", app)
        app.navigationBars.buttons.element(boundBy: 0).tap()
        let manifold = app.buttons["signal-manifold_kpa"]
        for _ in 0..<4 where !manifold.isHittable { app.swipeUp() }
        XCTAssertTrue(manifold.waitForExistence(timeout: 5))
        XCTAssertTrue(manifold.label.contains("Stale reading"))
        manifold.tap()
        XCTAssertTrue(app.descendants(matching: .any)["signalHistoryChart"].waitForExistence(timeout: 5))
        capture("Manifold timestamped history", app)
        app.buttons["signalStatistic"].tap()
        app.buttons["Period maximum"].tap()
        XCTAssertTrue(app.staticTexts["Period maximum · bucket averages and range"].waitForExistence(timeout: 5))
        capture("Manifold aggregate history", app)
        app.terminate()
        app.launchArguments = ["--ui-testing", "--ui-testing-signals", "--ui-testing-preserve-cache", "--ui-testing-offline"]
        app.launch()
        XCTAssertTrue(app.staticTexts["Saved on this phone"].waitForExistence(timeout: 5))
        app.staticTexts["Synthetic route truck"].tap()
        for _ in 0..<4 where !app.buttons["signal-fuel_level_pct"].isHittable { app.swipeUp() }
        app.buttons["signal-fuel_level_pct"].tap()
        XCTAssertTrue(app.staticTexts["Saved history on this phone"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.descendants(matching: .any)["signalHistoryChart"].exists)
        capture("Offline saved fuel history", app)
    }

    func testDashboardMetricVisibilityPersistsAndCanBeRestoredOffline() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-testing", "--ui-testing-signals"]
        app.launch()
        connect(app)
        app.staticTexts["Synthetic route truck"].tap()
        reveal(app.buttons["customizeMetrics"], app)
        app.buttons["customizeMetrics"].tap()
        let fuel = app.switches["metric-visible-fuel_level_pct"]
        XCTAssertTrue(fuel.waitForExistence(timeout: 5))
        fuel.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)).tap()
        XCTAssertEqual(fuel.value as? String, "0")
        capture("Choose dashboard metrics", app)
        app.buttons["metricsDone"].tap()
        XCTAssertFalse(app.buttons["signal-fuel_level_pct"].exists)
        app.terminate()
        app.launchArguments = ["--ui-testing", "--ui-testing-signals", "--ui-testing-preserve-cache", "--ui-testing-offline"]
        app.launch()
        XCTAssertTrue(app.staticTexts["Saved on this phone"].waitForExistence(timeout: 5))
        app.staticTexts["Synthetic route truck"].tap()
        reveal(app.buttons["customizeMetrics"], app)
        XCTAssertFalse(app.buttons["signal-fuel_level_pct"].exists)
        app.buttons["customizeMetrics"].tap()
        XCTAssertTrue(fuel.waitForExistence(timeout: 5))
        XCTAssertEqual(fuel.value as? String, "0")
        fuel.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)).tap()
        XCTAssertEqual(fuel.value as? String, "1")
        app.buttons["metricsDone"].tap()
        XCTAssertTrue(app.buttons["signal-fuel_level_pct"].waitForExistence(timeout: 5))
        capture("Hidden metric restored while offline", app)
    }

    func testSignalHistoryErrorAndEmptyStates() {
        for scenario in ["--ui-testing-history-error", "--ui-testing-history-empty"] {
            let app = XCUIApplication()
            app.launchArguments = ["--ui-testing", "--ui-testing-signals", scenario]
            app.launch()
            connect(app)
            app.staticTexts["Synthetic route truck"].tap()
            let fuel = app.buttons["signal-fuel_level_pct"]
            XCTAssertTrue(fuel.waitForExistence(timeout: 5))
            if !fuel.isHittable { app.swipeUp() }
            fuel.tap()
            if scenario == "--ui-testing-history-error" {
                XCTAssertTrue(app.buttons["Retry history"].waitForExistence(timeout: 5))
                XCTAssertFalse(app.staticTexts["Saved on this phone"].exists)
                capture("Signal history local error", app)
            } else {
                XCTAssertTrue(app.staticTexts["No readings in this range"].waitForExistence(timeout: 5))
                capture("Signal history empty range", app)
            }
            app.terminate()
        }
    }

    func testDiscreteStatesCodesAndCountHistory() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-testing", "--ui-testing-signals"]
        app.launch()
        connect(app)
        app.staticTexts["Synthetic route truck"].tap()
        for (metric, chart, title) in [
            ("fuel_system_1_status", "signalCodeChart", "Categorical codes without fractional states"),
            ("mil_on", "signalStateChart", "Boolean observed states and mixed bucket"),
            ("retained_samples", "signalBarChart", "Count bars preserve average and range")
        ] {
            let button = app.buttons["signal-\(metric)"]
            for _ in 0..<7 where !button.isHittable { app.swipeUp() }
            XCTAssertTrue(button.waitForExistence(timeout: 5))
            button.tap()
            XCTAssertTrue(app.descendants(matching: .any)[chart].waitForExistence(timeout: 5))
            XCTAssertFalse(app.descendants(matching: .any)["signalHistoryChart"].exists)
            XCTAssertFalse(app.descendants(matching: .any)["calendarSignalChart"].exists)
            if metric != "retained_samples" {
                XCTAssertTrue(app.staticTexts["Observed states · Mixed = multiple states"].exists)
            } else {
                XCTAssertTrue(app.staticTexts["Average per bucket · whiskers show range"].exists)
            }
            capture(title, app)
            app.navigationBars.buttons.element(boundBy: 0).tap()
        }
    }

    func testPiPairingUpdatesAndRevocation() {
        let app = openIntegrations()
        app.buttons["pairPi"].tap()
        let name = app.textFields["piName"]
        XCTAssertTrue(name.waitForExistence(timeout: 5))
        name.tap(); name.typeText("Synthetic collector")
        app.buttons["Create token"].tap()
        let token = app.staticTexts["pairingToken"]
        XCTAssertTrue(token.waitForExistence(timeout: 5))
        XCTAssertEqual(token.label, "synthetic-pairing-token-not-a-real-secret")
        capture("Pi one-time pairing with synthetic token", app)
        app.buttons["Done"].tap()
        XCTAssertTrue(token.waitForNonExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Waiting for pairing"].waitForExistence(timeout: 5))
        let updates = app.switches["deviceAutoUpdate-fixture-device"]
        reveal(updates, app)
        XCTAssertEqual(updates.value as? String, "1")
        // SwiftUI exposes the whole labeled row as the switch; its center misses the control.
        updates.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)).tap()
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in updates.value as? String == "0" }, object: nil)], timeout: 5), .completed)
        let gps = app.switches["deviceGPS-fixture-device"]
        reveal(gps, app)
        XCTAssertEqual(gps.value as? String, "0")
        gps.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)).tap()
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in gps.value as? String == "1" }, object: nil)], timeout: 5), .completed)
        reveal(app.staticTexts["GPS, GPS receiver not connected"], app)
        capture("Pi GPS opt-in awaiting receiver", app)
        capture("Pi waiting for pairing with updates paused", app)
        app.navigationBars.buttons.element(boundBy: 0).tap()
        let integrations = app.buttons["vehicleIntegrations"]
        XCTAssertTrue(integrations.waitForExistence(timeout: 5))
        integrations.tap()
        XCTAssertTrue(app.buttons["pairPi"].waitForExistence(timeout: 5))
        reveal(updates, app)
        XCTAssertEqual(updates.value as? String, "0")
        let revoke = app.buttons["revokePi-fixture-device"]
        reveal(revoke, app); revoke.tap()
        let confirmRevoke = app.sheets["Revoke this Pi's access?"].buttons["confirmRevokePi"].firstMatch
        XCTAssertTrue(confirmRevoke.waitForExistence(timeout: 5))
        confirmRevoke.tap()
        XCTAssertTrue(app.staticTexts["Access revoked"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.switches["deviceAutoUpdate-fixture-device"].exists)
        capture("Pi access revoked keeps previous readings", app)
    }

    func testSmartcarSelectionProvisioningAndDisconnect() {
        let app = openIntegrations()
        let connect = app.buttons["connectSmartcar"]
        reveal(connect, app)
        XCTAssertTrue(app.staticTexts["Simulated vehicle data"].exists)
        connect.tap()
        let candidate = app.buttons["smartcarCandidate-synthetic-candidate"]
        reveal(candidate, app)
        XCTAssertTrue(candidate.label.contains("2024 Sample Roadster"))
        capture("Smartcar explicit vehicle selection", app)
        candidate.tap()
        XCTAssertTrue(app.staticTexts["Waiting for vehicle data"].waitForExistence(timeout: 5))
        capture("Smartcar provisioning without invented readings", app)
        let sync = app.buttons["syncSmartcar"]
        reveal(sync, app); sync.tap()
        XCTAssertTrue(app.staticTexts["Vehicle linked"].waitForExistence(timeout: 5))
        capture("Smartcar observed and checked times remain separate", app)
        let disconnect = app.buttons["disconnectSmartcar"]
        reveal(disconnect, app); disconnect.tap()
        let confirmDisconnect = app.sheets["Disconnect Smartcar?"].buttons["confirmDisconnectSmartcar"].firstMatch
        XCTAssertTrue(confirmDisconnect.waitForExistence(timeout: 5))
        confirmDisconnect.tap()
        XCTAssertTrue(app.staticTexts["Not linked"].waitForExistence(timeout: 5))
        XCTAssertFalse(app.buttons["syncSmartcar"].exists)
    }

    func testIntegrationUnavailableStaleAndCancelledStates() {
        let app = openIntegrations(extra: ["--ui-testing-device-stale", "--ui-testing-smartcar-reconnect", "--ui-testing-smartcar-cancel"])
        XCTAssertTrue(app.staticTexts["Check-in overdue"].waitForExistence(timeout: 5))
        capture("Pi overdue heartbeat and rejected samples", app)
        let reconnect = app.buttons["connectSmartcar"]
        reveal(reconnect, app)
        XCTAssertEqual(reconnect.label, "Reconnect Smartcar")
        capture("Smartcar reconnect needed", app)
        reconnect.tap()
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in reconnect.isEnabled }, object: nil)], timeout: 5), .completed)
        XCTAssertFalse(app.staticTexts["integrationError"].exists)
        XCTAssertFalse(app.buttons["smartcarCandidate-synthetic-candidate"].exists)
        app.terminate()
        let unconfigured = openIntegrations(extra: ["--ui-testing-smartcar-unconfigured"])
        let message = unconfigured.staticTexts["Smartcar is not configured on your server yet."]
        reveal(message, unconfigured)
        XCTAssertFalse(unconfigured.buttons["connectSmartcar"].exists)
        capture("Smartcar server configuration required", unconfigured)
    }

    private func openIntegrations(extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-testing", "--ui-testing-integrations"] + extra
        app.launch(); connect(app)
        app.staticTexts["Synthetic route truck"].tap()
        app.buttons["garageMenu"].tap()
        let integrations = app.buttons["vehicleIntegrations"]
        XCTAssertTrue(integrations.waitForExistence(timeout: 5))
        integrations.tap()
        XCTAssertTrue(app.buttons["pairPi"].waitForExistence(timeout: 5))
        return app
    }

    func testCockpitMenuPhotoIdentityAndGPSPrivacyJourney() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-testing", "--ui-testing-cockpit"]
        app.launch(); connect(app)
        app.buttons["garageMenu"].tap()
        app.buttons["menuSettings"].tap()
        XCTAssertTrue(app.navigationBars["Settings"].waitForExistence(timeout: 5))
        capture("Garage settings in native menu", app)
        app.navigationBars["Settings"].buttons["Done"].tap()
        app.navigationBars["Garage menu"].buttons["Done"].tap()
        app.staticTexts["Synthetic route truck"].tap()
        XCTAssertTrue(app.staticTexts["VIN, SYNTHETIC-VIN"].waitForExistence(timeout: 5))
        capture("Vehicle photo and inline identity", app)
        app.buttons["garageMenu"].tap()
        app.buttons["Edit vehicle"].tap()
        XCTAssertTrue(app.buttons["chooseVehiclePhoto"].waitForExistence(timeout: 5))
        app.buttons["Remove photo"].tap()
        let vin = app.textFields["vehicleVIN"]
        reveal(vin, app)
        // The center of a trailing-aligned field can place the cursor before its text.
        vin.coordinate(withNormalizedOffset: CGVector(dx: 0.99, dy: 0.5)).tap()
        vin.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: "SYNTHETIC-VIN".count) + "UPDATED-VIN")
        // Keyboard events can finish after typeText returns on the release simulator.
        expectation(for: NSPredicate(format: "value == %@", "UPDATED-VIN"), evaluatedWith: vin)
        waitForExpectations(timeout: 5)
        let plate = app.textFields["vehicleLicensePlate"]
        reveal(plate, app)
        plate.coordinate(withNormalizedOffset: CGVector(dx: 0.99, dy: 0.5)).tap()
        plate.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: "TEST-ONLY".count) + "NEW-PLATE")
        expectation(for: NSPredicate(format: "value == %@", "NEW-PLATE"), evaluatedWith: plate)
        waitForExpectations(timeout: 5)
        capture("Editable VIN and license plate", app)
        app.buttons["Save"].tap()
        XCTAssertTrue(vin.waitForNonExistence(timeout: 5))
        app.buttons["Done"].tap()
        XCTAssertTrue(app.staticTexts["VIN, UPDATED-VIN"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["License plate, NEW-PLATE"].exists)
        XCTAssertFalse(app.descendants(matching: .any)["vehiclePhoto"].exists)
        reveal(app.buttons["Trips"], app); app.buttons["Trips"].tap()
        let location = app.buttons["vehicleLocationMap"]
        reveal(location, app)
        XCTAssertTrue(app.staticTexts["Stale"].exists)
        capture("Last known GPS location with explicit stale time", app)
        location.tap()
        XCTAssertTrue(app.navigationBars["Vehicle location"].waitForExistence(timeout: 5))
        capture("Last known location map", app)
        app.buttons["Done"].tap()
        reveal(app.staticTexts["Synthetic park loop"], app); app.staticTexts["Synthetic park loop"].tap()
        XCTAssertTrue(app.staticTexts["Estimated from GPS fixes. Separate from the odometer."].waitForExistence(timeout: 5))
        capture("GPS route with derived distance", app)
        app.navigationBars.buttons.element(boundBy: 0).tap()
        app.buttons["garageMenu"].tap()
        app.buttons["clearGPSHistory"].tap()
        app.sheets["Delete GPS location history?"].buttons["Delete saved GPS history"].firstMatch.tap()
        reveal(app.staticTexts["No location reported yet"], app)
        XCTAssertFalse(app.staticTexts["Synthetic park loop"].exists)
        capture("Deleted GPS location history", app)
    }

    func testSmartcarParkedMapWithoutTripsAndSeparatePiRoutes() {
        let app = XCUIApplication()
        app.launchArguments = ["--ui-testing", "--ui-testing-cockpit", "--ui-testing-smartcar-location"]
        app.launch(); connect(app)
        app.staticTexts["Synthetic connected car"].tap()
        reveal(app.buttons["Trips"], app); app.buttons["Trips"].tap()
        let location = app.buttons["vehicleLocationMap"]
        reveal(location, app)
        XCTAssertTrue(app.staticTexts["Smartcar · Last parked"].exists)
        XCTAssertFalse(app.staticTexts["Synthetic park loop"].exists)
        capture("Smartcar last parked location without a Pi", app)
        location.tap()
        XCTAssertTrue(app.navigationBars["Vehicle location"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["Smartcar · Last parked"].exists)
        XCTAssertTrue(app.staticTexts["Smartcar reported where the vehicle last parked. It may have moved since then."].exists)
        capture("Expanded Smartcar parked map", app)
        app.buttons["Done"].tap()
        reveal(app.staticTexts["No recorded trips yet"], app)
        XCTAssertFalse(app.staticTexts["Synthetic park loop"].exists)
        capture("Smartcar location with separate empty trip history", app)

        app.navigationBars.buttons.element(boundBy: 0).tap()
        XCTAssertTrue(app.staticTexts["Synthetic route truck"].waitForExistence(timeout: 5))
        app.staticTexts["Synthetic route truck"].tap()
        reveal(app.buttons["Trips"], app); app.buttons["Trips"].tap()
        reveal(app.buttons["vehicleLocationMap"], app)
        XCTAssertTrue(app.staticTexts["Pi GPS · GPS fix"].exists)
        XCTAssertFalse(app.staticTexts["Smartcar · Last parked"].exists)
        reveal(app.staticTexts["Synthetic park loop"], app)
        app.staticTexts["Synthetic park loop"].tap()
        XCTAssertTrue(app.staticTexts["4 recorded location samples"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "Recorded trip route")).firstMatch.exists)
        XCTAssertTrue(app.staticTexts["Estimated from GPS fixes. Separate from the odometer."].exists)
        capture("Pi trip route remains separate from Smartcar vehicle", app)
    }

    private func reveal(_ element: XCUIElement, _ app: XCUIApplication) {
        for _ in 0..<7 where !element.isHittable { app.swipeUp() }
        XCTAssertTrue(element.waitForExistence(timeout: 5))
        XCTAssertTrue(element.isHittable)
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
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
