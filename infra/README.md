# Infrastructure (for the infra agent)

Everything acat needs outside the repo. Both items are idempotent: re-applying must converge, not duplicate.

## 1. Entra app registration

Desired state: [`entra-app.json`](entra-app.json).

Steps:
1. Find or create the app `acat` (match by `displayName`; if more than one exists, stop and ask).
2. Set `signInAudience=AzureADMultipleOrgs`, `isFallbackPublicClient=true`, public client redirect URI `http://localhost`.
3. For each entry in `requiredResourceAccess`, resolve the service principal of the resource:
   - Microsoft Graph: `az ad sp show --id 00000003-0000-0000-c000-000000000000`
   - Azure Communication Services: `az ad sp list --filter "servicePrincipalNames/any(n:n eq 'https://auth.msft.communication.azure.com')"`
   Map each scope value to its GUID via `oauth2PermissionScopes[].value`. Add them as `type: Scope`.
4. Do **not** create a client secret or certificate (public client).
5. Optional, for the home tenant: grant admin consent so test users skip the prompt.

Output: `ACAT_CLIENT_ID` (the appId). It is not a secret.

## 2. Azure Communication Services

Deploy [`acs.bicep`](acs.bicep) into a resource group of your choice (data location `Europe`).

Output: `ACAT_ACS_CONNECTION_STRING`. **This is a secret.** Store it only in the relay's secret store and never in the repo.

## 3. Relay host

See [`../docs/self-hosting.md`](../docs/self-hosting.md). Needs a public DNS name with valid TLS. Microsoft Graph must be able to reach `https://<domain>/hook` and `https://<domain>/lifecycle`.
