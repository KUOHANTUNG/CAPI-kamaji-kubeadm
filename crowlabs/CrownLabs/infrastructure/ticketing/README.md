# Ticketing assistance - Faveo helpdesk

Faveo is an open source ticket based support system with knowledge base. It’s specifically designed to cater the needs of Startup's & SME's empowering them with state of art, ticket based support system.

<!-- markdown-link-check-disable -->
More info at [Faveo's website](https://www.faveohelpdesk.com)
<!-- markdown-link-check-enable -->
### Pre-requisites
Here we assume that exists a namespace in K8S cluster called **crownlabs-ticketing**

### Install Procedure
Now we can proceed by installing Faveo helpdesk by applying the following manifests:
* [mysql-secret.yaml](manifests/mysql-secret.yaml), it will create the `faveo-db-auth` secret, which contains custom encoded values for `DB_DATABASE`, `DB_USERNAME` and `DB_PASSWORD`;
* [faveo-mysql-cluster-manifest.yaml](manifests/faveo-mysql-cluster-manifest.yaml), to expose a mysql instance for the database, this needs the `faveo-db-auth` secret to be already applied in the namespace;
* [faveo-ingress.yaml](manifests/faveo-ingress.yaml), to expose faveo on Internet, it will be available [here](https://support.crownlabs.polito.it);
* [faveo-php-configmap.yaml](manifests/faveo-php-configmap.yaml), which contains environment variables for faveo, the following parameters have to be configured
    * `DB_DATABASE` insert here the database name, it can be retrieved from the `faveo-db-auth` secret
    * `DB_USERNAME` insert here the username of the database owner, it can be retrieved from the `faveo-db-auth` secret
    * `DB_PASSWORD` insert here the password of the database owner, it can be retrieved from the `faveo-db-auth` secret
    * `ADMIN_USERNAME` insert here username for the first admin user created
    * `ADMIN_PASSWORD` insert here password for the first admin user created
    * `JWT_SECRET` generate a 32-character string, or launch the `php artisan jwt:secret` command
* [faveo-service.yaml](manifests/faveo-service.yaml), with clusterIP service for the deployment;
* [faveo-deployment.yaml](manifests/faveo-deployment.yaml), for create and start a container with faveo.

```bash
kubectl apply -f mysql-secret.yaml
kubectl apply -f faveo-mysql-cluster-manifest.yaml
kubectl apply -f faveo-ingress.yaml
kubectl apply -f faveo-php-configmap.yaml
kubectl apply -f faveo-service.yaml
kubectl apply -f faveo-deployment.yaml
```
### OIDC Login
In this version of Faveo helpdesk we offer authentication through Keycloak. To configure it, or another openid-connect provider insert `base URL`, `Client ID` and `Client secret` in the Social setting section of Admin panel like for the other socialite providers
