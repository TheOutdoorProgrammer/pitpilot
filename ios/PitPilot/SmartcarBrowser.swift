import AuthenticationServices
import UIKit

@MainActor
protocol SmartcarAuthenticating {
    func authenticate(_ session: SmartcarSession) async throws -> URL
    func cancel()
}

@MainActor
final class SmartcarBrowser: NSObject, SmartcarAuthenticating, ASWebAuthenticationPresentationContextProviding {
    private var session: ASWebAuthenticationSession?
    private var continuation: CheckedContinuation<URL, Error>?
    private var anchor: UIWindow?
    private var sessionID: UUID?

    func authenticate(_ request: SmartcarSession) async throws -> URL {
        let state = try request.validate()
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("--ui-testing"), ProcessInfo.processInfo.arguments.contains("--ui-testing-integrations") {
            if ProcessInfo.processInfo.arguments.contains("--ui-testing-smartcar-cancel") { throw CancellationError() }
            var callback = URLComponents()
            callback.scheme = request.callbackScheme; callback.host = "callback"
            callback.queryItems = [URLQueryItem(name: "state", value: state), URLQueryItem(name: "user_id", value: "synthetic-user")]
            return callback.url!
        }
        #endif
        guard session == nil else { throw APIError.message("A Smartcar sign-in is already open.") }
        guard let window = UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene })
            .filter({ $0.activationState == .foregroundActive }).flatMap(\.windows).first(where: \.isKeyWindow) else {
            throw APIError.message("Return to PitPilot and start Smartcar sign-in again.")
        }
        anchor = window
        let id = UUID()
        sessionID = id
        return try await withTaskCancellationHandler {
            try Task.checkCancellation()
            return try await withCheckedThrowingContinuation { continuation in
                self.continuation = continuation
                let completion: ASWebAuthenticationSession.CompletionHandler = { [weak self] url, error in
                    Task { @MainActor in
                        guard self?.sessionID == id else { return }
                        if (error as? ASWebAuthenticationSessionError)?.code == .canceledLogin {
                            self?.finish(.failure(CancellationError()))
                        } else if error != nil || url == nil {
                            self?.finish(.failure(APIError.message("Smartcar sign-in could not finish. Please try again.")))
                        } else if let url { self?.finish(.success(url)) }
                    }
                }
                let browser: ASWebAuthenticationSession
                if #available(iOS 17.4, *) {
                    browser = ASWebAuthenticationSession(url: request.authorizationUrl, callback: .customScheme(request.callbackScheme), completionHandler: completion)
                } else {
                    browser = ASWebAuthenticationSession(url: request.authorizationUrl, callbackURLScheme: request.callbackScheme, completionHandler: completion)
                }
                browser.presentationContextProvider = self
                browser.prefersEphemeralWebBrowserSession = true
                session = browser
                if !browser.start() { finish(.failure(APIError.message("Smartcar sign-in could not open. Please try again."))) }
            }
        } onCancel: { Task { @MainActor in if self.sessionID == id { self.cancel() } } }
    }

    func cancel() {
        session?.cancel()
        finish(.failure(CancellationError()))
    }

    private func finish(_ result: Result<URL, Error>) {
        let waiting = continuation
        continuation = nil; session = nil; anchor = nil; sessionID = nil
        waiting?.resume(with: result)
    }

    func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor { anchor ?? ASPresentationAnchor() }
}
