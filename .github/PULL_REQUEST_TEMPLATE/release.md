## Checklist

### Release Note

- [ ] Review [the $RELEASE_VERSION release note draft]($RELEASE_NOTE_URL).
- [ ] Ask for its review to the APM, ASM and Profiling team leads on the `#guild-dd-go` slack channel.

### Documentation

- [ ] If required by this release, update the [public documentation](https://github.com/DataDog/documentation).

### Release Candidate

- [ ] Create a release candidate version on this release branch. This is automatically performed by the github workflow 
  triggered by the label `release/publish-new-rc` which will release a new release candidate tag with the latest changes
  of the release branch.

- Deploy to Datadog's staging services:
  - [ ] Give a heads-up on the slack channel `#staging-headsup` that you are starting to work on the deployment.
  - [ ] Create a pull request on the `dd-go` repository upgrading `dd-trace-go` to the `$RELEASE_VERSION`
    release-candidate version you want to deploy.  
    For example:
    ```console
    # On your dd-go repository clone
    dd-go/$ git checkout prod
    dd-go/$ git pull
    dd-go/$ git checkout -b dd-trace-go-$RELEASE_VERSION
    dd-go/$ go get -u -v gopkg.in/DataDog/dd-trace-go.v1@$RELEASE_VERSION-rc.4
    dd-go/$ go mod tidy
    dd-go/$ git add go.mod go.sum
    dd-go/$ git commit -m '[go.mod] upgrade dd-trace-go to $RELEASE_VERSION-rc.4'
    dd-go/$ git push -u origin dd-trace-go-$RELEASE_VERSION
    dd-go/$ gh pr create
    ```
  - [ ] Deploy to Datadog's staging services:
    ```console
    dd-go/$ env GITLAB_TOKEN=MY_TOKEN to-staging <the services you want to redeploy>
    ```
  - [ ] Check the deployment is successful on the [Go tracer dashboard] where the number of running services with the
    release candidate version should start to show up and increase up to the number of services being deployed.

- Deploy the reliability-environment services:
  - [ ] Run [the gitlab pipeline](https://gitlab.ddbuild.io/DataDog/datadog-reliability-env/-/pipelines/new)
    with the variable `DD_TRACE_GO_CANDIDATE_VERSION` set to the release candidate version you want to deploy.
  - [ ] Check the deployment is successful on the [Go rel-env dashboard] where the number of running services with the
    release candidate version should start to show up and increase.

### Validating the release

- Let the release candidate run on staging and rel-env for at least 24 hours and review all the dashboards, especially:
  - [ ] Check the memory usage trend of rel-env which should be flat over time on the [Go rel-env dashboard].
  - [ ] Check the deployment tab of some deployed staging services to see if there was any negative impact on the
    service latency and memory usage (eg. https://ddstaging.datadoghq.com/apm/services/trace-edge/operations/http.request/deployments).
  - [ ] Check through the [Go tracer dashboard] to look for any anomalies such as high memory or cpu use, increased
    number of dropped traces, increase in tracer errors.

- [ ] Give a heads-up to the APM, ASM and Profiling teams on `#guild-dd-go` so that they can perform any further checks
  of the newly deployed release candidate version. They should acknowledge the fact everything is good and the release
  process can move forward.

- If anything went wrong during this release candidate validation, you can fix the problems on this same release branch
  and repeat the previous steps to publish and deploy a new release candidate version.

### Finishing the release

- [ ] Use the pull request label `bot/release/merge` in order to automatically merge the release branch into the `main`
  and `v1` branches, and update the version file of the `main` branch to the next minor version.
- [ ] Publish the git tag `$RELEASE_VERSION` by publishing [the $RELEASE_VERSION release note draft]($RELEASE_NOTE_URL).
- [ ] Finish updating dd-go's `go.mod` file now to the final `$RELEASE_VERSION` tag and merge your pull request.
- [ ] Give a heads-up on the slack channels `#go` and `#guild-dd-go` about the now released `$RELEASE_VERSION` and
 `dd-go` update.
- [ ] [Create the github milestone](https://github.com/DataDog/dd-trace-go/milestones/new) for the next version `$NEXT_MINOR_RELEASE_VERSION`.
- [ ] Close the now released milestone and move its still opened PRs and issues to `Triage` or the new milestone `$NEXT_MINOR_RELEASE_VERSION`.

[Go tracer dashboard]: https://ddstaging.datadoghq.com/dashboard/r92-2p7-shv/go-tracer
[Go rel-env dashboard]: https://ddstaging.datadoghq.com/dashboard/s2a-5wy-g5b/go-reliability-env-dashboard
