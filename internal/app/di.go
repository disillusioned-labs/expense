package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/disillusioned-labs/platform/s3"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/disillusioned-labs/platform/authkit"
	"github.com/disillusioned-labs/platform/cache"

	"github.com/disillusioned-labs/expense/internal/authz"
	"github.com/disillusioned-labs/expense/internal/config"
	"github.com/disillusioned-labs/expense/internal/contract"
	"github.com/disillusioned-labs/expense/internal/handler"
	approvalhandler "github.com/disillusioned-labs/expense/internal/handler/approval"
	documenthandler "github.com/disillusioned-labs/expense/internal/handler/document"
	projecthandler "github.com/disillusioned-labs/expense/internal/handler/project"
	rolehandler "github.com/disillusioned-labs/expense/internal/handler/role"
	transactionhandler "github.com/disillusioned-labs/expense/internal/handler/transaction"
	"github.com/disillusioned-labs/expense/internal/ocr"
	"github.com/disillusioned-labs/expense/internal/repository"
	"github.com/disillusioned-labs/expense/internal/server"
	approvalservice "github.com/disillusioned-labs/expense/internal/service/approval"
	documentservice "github.com/disillusioned-labs/expense/internal/service/document"
	projectservice "github.com/disillusioned-labs/expense/internal/service/project"
	roleservice "github.com/disillusioned-labs/expense/internal/service/role"
	transactionservice "github.com/disillusioned-labs/expense/internal/service/transaction"
)

func buildDeps(
	ctx context.Context,
	pool *pgxpool.Pool,
	rdb *goredis.Client,
	redisRequired bool,
	svcCache cache.Cache,
	storage *s3.Client,
	identityClient contract.IdentityClient,
	ocrSubmitter ocr.Submitter,
	cfg *config.Config,
	log *slog.Logger,
) (server.Deps, error) {
	repo := repository.NewStore(pool)

	authErrorHandler := func(w http.ResponseWriter, _ *http.Request, _ error) {
		handler.WriteError(w, http.StatusUnauthorized, handler.CodeUnauthorized, "unauthorized")
	}
	verifier := authkit.New(
		authkit.Config{
			Issuer:  cfg.Auth.Issuer,
			JWKSURL: cfg.Auth.JWKSURL,
		},
		authkit.WithErrorHandler(authErrorHandler),
		authkit.WithLogger(log),
	)

	authorizer := authz.NewAuthorizer(repo, identityClient, log)

	roleSvc := roleservice.NewRoleService(repo, log)
	projectSvc := projectservice.NewProjectService(repo, authorizer, roleSvc, log)

	transactionSvc := transactionservice.NewTransactionService(repo, authorizer, storage, identityClient, log)

	documentSvc := documentservice.NewDocumentService(repo, authorizer, storage, ocrSubmitter, identityClient, log)

	approvalSvc := approvalservice.NewApprovalService(repo, authorizer, identityClient, log)

	return server.Deps{
		Pool:             pool,
		Redis:            rdb,
		RedisRequired:    redisRequired,
		Cache:            svcCache,
		Verifier:         verifier,
		AuthErrorHandler: authErrorHandler,

		RoleHandler:        rolehandler.NewRoleHandler(roleSvc, authorizer, log),
		ProjectHandler:     projecthandler.NewProjectHandler(projectSvc, authorizer, log),
		TransactionHandler: transactionhandler.NewTransactionHandler(transactionSvc, log),
		DocumentHandler:    documenthandler.NewDocumentHandler(documentSvc, log),
		ApprovalHandler:    approvalhandler.NewApprovalHandler(approvalSvc, log),
	}, nil
}
