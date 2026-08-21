package azure

import (
	"fmt"

	"github.com/gecburton/cloudcompose/internal/compiler/shared"
	"github.com/gecburton/cloudcompose/internal/models"
)

// appPerAppCIDRNewbits and appSubnetNewbits are the two levels of CIDR
// carving: AppsCIDR is a /17 (half the VNet); each app gets its own /24
// (newbits=7, netnum=SubnetIndex) out of that; each app's own four
// subnets are /26s (newbits=2, netnum=0..3) carved out of its /24. /26 =
// 64 addresses, double Container Apps' documented /27 minimum. This
// supports up to 128 apps per Cloud Compose Environment at the default
// /16 VNet size.
const (
	appPerAppCIDRNewbits = 7
	appSubnetNewbits     = 2
)

// appSubnetsAzure creates this app's Container Apps Environment and its
// four delegated subnets (infrastructure/postgresql/mysql/redis), carved
// out of env.AppsCIDR at env.SubnetIndex. Sets
// env.InfrastructureSubnetID/PostgresqlSubnetID/MysqlSubnetID/
// RedisSubnetID for downstream inference functions to consume.
//
// Must run before anything that reads those four fields, or the
// Container App/Job resources that reference this environment's ID.
func appSubnetsAzure(
	resources *models.AzureResources,
	app *models.Application,
	env *models.AzureEnvironment,
	getName func(string) string,
	tags map[string]string,
) error {
	appCIDR, err := shared.Cidrsubnet(env.AppsCIDR, appPerAppCIDRNewbits, env.SubnetIndex)
	if err != nil {
		return fmt.Errorf("app %q's --subnet-index=%d could not be carved from the environment's apps_cidr %q: %w", app.Name, env.SubnetIndex, env.AppsCIDR, err)
	}

	infraCIDR, err := shared.Cidrsubnet(appCIDR, appSubnetNewbits, 0)
	if err != nil {
		return err
	}
	pgCIDR, err := shared.Cidrsubnet(appCIDR, appSubnetNewbits, 1)
	if err != nil {
		return err
	}
	mysqlCIDR, err := shared.Cidrsubnet(appCIDR, appSubnetNewbits, 2)
	if err != nil {
		return err
	}
	redisCIDR, err := shared.Cidrsubnet(appCIDR, appSubnetNewbits, 3)
	if err != nil {
		return err
	}

	delegation := func(delegationName, serviceName string) []models.SubnetDelegation {
		return []models.SubnetDelegation{{
			Name: delegationName,
			ServiceDelegation: []models.ServiceDelegation{{
				Name:    serviceName,
				Actions: []string{"Microsoft.Network/virtualNetworks/subnets/join/action"},
			}},
		}}
	}

	resources.Subnet["infrastructure"] = models.Subnet{
		Name:               getName("infrastructure"),
		ResourceGroupName:  env.ResourceGroupName,
		VirtualNetworkName: env.VnetName,
		AddressPrefixes:    []string{infraCIDR},
		Delegation:         delegation("container-apps", "Microsoft.App/environments"),
	}
	resources.Subnet["postgresql"] = models.Subnet{
		Name:               getName("postgresql"),
		ResourceGroupName:  env.ResourceGroupName,
		VirtualNetworkName: env.VnetName,
		AddressPrefixes:    []string{pgCIDR},
		Delegation:         delegation("postgresql-flexible-server", "Microsoft.DBforPostgreSQL/flexibleServers"),
	}
	resources.Subnet["mysql"] = models.Subnet{
		Name:               getName("mysql"),
		ResourceGroupName:  env.ResourceGroupName,
		VirtualNetworkName: env.VnetName,
		AddressPrefixes:    []string{mysqlCIDR},
		Delegation:         delegation("mysql-flexible-server", "Microsoft.DBforMySQL/flexibleServers"),
	}
	// Not delegated: azurerm_private_endpoint (Managed Redis) attaches
	// to a plain subnet, unlike the delegated subnets Flexible Server
	// needs above.
	resources.Subnet["redis"] = models.Subnet{
		Name:               getName("redis"),
		ResourceGroupName:  env.ResourceGroupName,
		VirtualNetworkName: env.VnetName,
		AddressPrefixes:    []string{redisCIDR},
	}

	infraSubnetID := "${azurerm_subnet.infrastructure.id}"
	resources.ContainerAppEnvironment["main"] = models.ContainerAppEnvironment{
		Name:                    getName("env"),
		ResourceGroupName:       env.ResourceGroupName,
		Location:                env.Region,
		LogAnalyticsWorkspaceID: env.LogAnalyticsWorkspaceID,
		InfrastructureSubnetID:  &infraSubnetID,
		Tags:                    tags,
	}

	env.InfrastructureSubnetID = infraSubnetID
	pgSubnetID := "${azurerm_subnet.postgresql.id}"
	env.PostgresqlSubnetID = &pgSubnetID
	mysqlSubnetID := "${azurerm_subnet.mysql.id}"
	env.MysqlSubnetID = &mysqlSubnetID
	redisSubnetID := "${azurerm_subnet.redis.id}"
	env.RedisSubnetID = &redisSubnetID

	return nil
}
